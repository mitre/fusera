// Copyright 2015 - 2017 Ka-Hing Cheung
// Modifications Copyright 2018 The MITRE Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fuseralib

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"sync"
	"syscall"
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/awsutil"
	"github.com/mitre/fusera/flags"
	"github.com/pkg/errors"

	"github.com/jacobsa/fuse"
)

type FileHandle struct {
	inode *Inode

	mpuKey    *string
	dirty     bool
	writeInit sync.Once
	mpuWG     sync.WaitGroup
	etags     []*string

	mu              sync.Mutex
	mpuID           *string
	nextWriteOffset int64
	lastPartID      int

	poolHandle *BufferPool
	buf        *MBuf

	lastWriteError error

	// read
	reader        io.ReadCloser
	readBufOffset int64

	// parallel read
	buffers           []*S3ReadBuffer
	existingReadahead int
	seqReadAmount     uint64
	numOOORead        uint64 // number of out of order read
}

const MaxReadAhead = uint32(100 * 1024 * 1024)
const ReadAheadChunk = uint32(20 * 1024 * 1024)

func NewFileHandle(in *Inode) *FileHandle {
	fh := &FileHandle{inode: in}
	return fh
}

type S3ReadBuffer struct {
	offset uint64
	size   uint32
	buf    *Buffer
}

func (b *S3ReadBuffer) Read(offset uint64, p []byte) (n int, err error) {
	if b.offset != offset {
		panic(fmt.Sprintf("not the right buffer, expecting %v got %v, %v left", b.offset, offset, b.size))
	}

	n, err = io.ReadFull(b.buf, p)
	if n != 0 && err == io.ErrUnexpectedEOF {
		err = nil
	}
	if n > 0 {
		if uint32(n) > b.size {
			panic(fmt.Sprintf("read more than available %v %v", n, b.size))
		}

		b.offset += uint64(n)
		b.size -= uint32(n)
	}
	return
}

func (fh *FileHandle) readFromReadAhead(offset uint64, buf []byte) (bytesRead int, err error) {
	var nread int
	for len(fh.buffers) != 0 {
		nread, err = fh.buffers[0].Read(offset+uint64(bytesRead), buf)
		bytesRead += nread
		if err != nil {
			return
		}

		if fh.buffers[0].size == 0 {
			// we've exhausted the first buffer
			fh.buffers[0].buf.Close()
			fh.buffers = fh.buffers[1:]
		}

		buf = buf[nread:]

		if len(buf) == 0 {
			// we've filled the user buffer
			return
		}
	}

	return
}

func (fh *FileHandle) ReadFile(offset int64, buf []byte) (bytesRead int, err error) {
	// fh.inode.logFuse("ReadFile", offset, len(buf))
	defer func() {
		// fh.inode.logFuse("< ReadFile", bytesRead, err)
		if err != nil {
			if err == io.EOF {
				err = nil
			}
		}
	}()

	fh.mu.Lock()
	defer fh.mu.Unlock()

	nwant := len(buf)
	var nread int

	for bytesRead < nwant && err == nil {
		nread, err = fh.readFile(offset+int64(bytesRead), buf[bytesRead:])
		if nread > 0 {
			bytesRead += nread
		}
	}

	return
}

func (fh *FileHandle) readFile(offset int64, buf []byte) (bytesRead int, err error) {
	defer func() {
		if bytesRead > 0 {
			fh.readBufOffset += int64(bytesRead)
			fh.seqReadAmount += uint64(bytesRead)
		}

		// fh.inode.logFuse("< readFile", bytesRead, err)
	}()

	if uint64(offset) >= fh.inode.Attributes.Size {
		twig.Debug("nothing to read")
		// nothing to read
		if fh.inode.Invalid {
			err = fuse.ENOENT
		} else if fh.inode.KnownSize == nil {
			err = io.EOF
		} else {
			err = io.EOF
		}
		return
	}

	fs := fh.inode.fs

	if fh.poolHandle == nil {
		fh.poolHandle = fs.bufferPool
	}

	if fh.readBufOffset != offset {
		// XXX out of order read, maybe disable prefetching
		// fh.inode.logFuse("out of order read", offset, fh.readBufOffset)

		fh.readBufOffset = offset
		fh.seqReadAmount = 0
		if fh.reader != nil {
			fh.reader.Close()
			fh.reader = nil
		}

		if fh.buffers != nil {
			// we misdetected
			fh.numOOORead++
		}

		for _, b := range fh.buffers {
			b.buf.Close()
		}
		fh.buffers = nil
	}

	bytesRead, err = fh.readFromStream(offset, buf)

	return
}

func (fh *FileHandle) Release() {
	// read buffers
	for _, b := range fh.buffers {
		b.buf.Close()
	}
	fh.buffers = nil

	if fh.reader != nil {
		fh.reader.Close()
	}

	// write buffers
	if fh.poolHandle != nil {
		if fh.buf != nil && fh.buf.buffers != nil {
			if fh.lastWriteError == nil {
				panic("buf not freed but error is nil")
			}

			fh.buf.Free()
			// the other in-flight multipart PUT buffers will be
			// freed when they finish/error out
		}
	}

	fh.inode.mu.Lock()
	defer fh.inode.mu.Unlock()

	if fh.inode.fileHandles == 0 {
		panic(fh.inode.fileHandles)
	}

	fh.inode.fileHandles--
}

// Returns the number of bytes read and a file error if one occured.
func (fh *FileHandle) readFromStream(offset int64, buf []byte) (bytesRead int, err error) {
	if uint64(offset) >= fh.inode.Attributes.Size {
		// nothing to read
		return
	}

	byteRange := ""
	if offset != 0 {
		byteRange = fmt.Sprintf("bytes=%v-", offset)
	}

	if fh.reader == nil {
		if fh.inode.ErrContents == "" {
			sd, _ := time.ParseDuration("30s")
			exp := fh.inode.Attributes.ExpirationDate
			if fh.inode.ReqPays {
				client := awsutil.NewClient(fh.inode.Bucket, fh.inode.Key, fh.inode.Platform.Region, fh.inode.fs.opt.Profile)
				body, err := client.GetObjectRange(byteRange)
				if err != nil {
					return 0, syscall.EACCES
				}
				fh.reader = body
			} else if fh.inode.Link == "" {
				// we need to get a link no matter what
				if flags.Verbose {
					fmt.Printf("seems like we don't have a url for: %s\n", *fh.inode.Name)
				}
				link, expiration, err := newURL(fh.inode)
				if err != nil {
					return 0, syscall.EACCES
				}
				fh.inode.Link = link
				fh.inode.Attributes.ExpirationDate = expiration
			} else if !exp.IsZero() && time.Until(exp) < sd {
				// so the expiration date isn't zero and it's about to expire
				if flags.Verbose {
					fmt.Printf("seems like we have a url that expires: %s\n", exp)
				}
				if time.Until(exp) < sd {
					if flags.Verbose {
						fmt.Println("url is expired")
					}
					// Time to hot swap urls!
					link, expiration, err := newURL(fh.inode)
					if err != nil {
						// fh.inode.logFuse("< readFromStream error", 0, err)
						return 0, syscall.EACCES
					}
					fh.inode.Link = link
					fh.inode.Attributes.ExpirationDate = expiration
				}
			}
			resp, err := awsutil.GetObjectRange(fh.inode.Link, byteRange)
			if err != nil {
				return 0, err
			}

			fh.reader = resp.Body
		} else {
			// This is an error.log file, need to read from its error contents.
			fh.reader = ioutil.NopCloser(bytes.NewBufferString(fh.inode.ErrContents))
		}
	}

	bytesRead, err = fh.reader.Read(buf)
	if err != nil {
		if flags.Verbose {
			fmt.Println("error reading file")
			fmt.Println(err.Error())
		}
		if err != io.EOF {
			twig.Debugf("readFromStream error: %s", err.Error())
			// fh.inode.logFuse("< readFromStream error", bytesRead, err)
		}
		// always retry error on read
		fh.reader.Close()
		fh.reader = nil
		err = nil
	}

	return
}

// TODO: If on GCP, we now need to get a new instance token everytime we want a new url
func newURL(inode *Inode) (string, time.Time, error) {
	accession, err := inode.fs.signer.Sign(inode.Acc)
	if err != nil {
		return "", time.Now(), errors.Wrapf(err, "issue contacting API while trying to renew signed url for:\naccession: %s\nfile: %s\n", inode.Acc, *inode.Name)
	}
	if flags.Verbose {
		fmt.Println("got a response from API")
	}
	for _, f := range accession.Files {
		if f.Name == *inode.Name {
			if f.Link == "" {
				return "", time.Now(), errors.Errorf("API did not give new signed url for:\naccession: %s\nfile: %s\n", inode.Acc, *inode.Name)
			}
			if flags.Verbose {
				fmt.Printf("got a new link: %s\n", f.Link)
			}
			return f.Link, f.ExpirationDate, nil
		}
	}
	if flags.Verbose {
		twig.Debug("did not get a new link")
	}
	return "", time.Now(), errors.Errorf("couldn't get new signed url for:\naccession: %s\nfile: %s\n", inode.Acc, *inode.Name)
}

func (fh *FileHandle) resetToKnownSize() {
	if fh.inode.KnownSize != nil {
		fh.inode.Attributes.Size = *fh.inode.KnownSize
	} else {
		fh.inode.Attributes.Size = 0
		fh.inode.Invalid = true
	}
}
