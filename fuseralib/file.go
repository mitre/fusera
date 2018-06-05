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
	"github.com/mitre/fusera/nr"
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
	mpuId           *string
	nextWriteOffset int64
	lastPartId      int

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

const MAX_READAHEAD = uint32(100 * 1024 * 1024)
const READAHEAD_CHUNK = uint32(20 * 1024 * 1024)

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
	if b.offset == offset {
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
	} else {
		panic(fmt.Sprintf("not the right buffer, expecting %v got %v, %v left", b.offset, offset, b.size))
		// err = errors.New(fmt.Sprintf("not the right buffer, expecting %v got %v", b.offset, offset))
		// return
	}
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

	fh.inode.fileHandles -= 1
}

// Returns the number of bytes read and a file error if one occured.
func (fh *FileHandle) readFromStream(offset int64, buf []byte) (bytesRead int, err error) {
	if uint64(offset) >= fh.inode.Attributes.Size {
		// nothing to read
		return
	}

	if fh.reader == nil {
		if fh.inode.ErrContents == "" {
			sd, _ := time.ParseDuration("30s")
			exp := fh.inode.Attributes.ExpirationDate
			if !exp.IsZero() {
				twig.Debugf("seems like we have a url that expires: %s", exp)
				if time.Until(exp) < sd {
					twig.Debug("url is expired")
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

			bytes := ""
			if offset != 0 {
				bytes = fmt.Sprintf("bytes=%v-", offset)
			}

			resp, err := awsutil.GetObjectRange(fh.inode.Link, bytes)
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
		twig.Debug("error reading file")
		twig.Debug(err.Error())
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

func newURL(inode *Inode) (string, time.Time, error) {
	errfmtstr := "\naccession: %s\nfile: %s\n"
	payload, err := nr.ResolveNames(inode.fs.opt.ApiEndpoint, 1, false, inode.fs.opt.Loc, inode.fs.opt.Ngc, map[string]bool{inode.Acc: true}, inode.fs.opt.Filetypes)
	if err != nil {
		return "", time.Now(), errors.Wrapf(err, "issue contacting API while trying to renew signed url for:%s", errfmtstr, inode.Acc, inode.Name)
	}
	twig.Debug("resolved a url")
	for _, p := range payload {
		for _, f := range p.Files {
			if f.Name == *inode.Name {
				twig.Debug("got a new link")
				if f.Link == "" {
					return "", time.Now(), errors.Errorf("API did not give new signed url for:%s", errfmtstr, inode.Acc, inode.Name)
				}
				return f.Link, f.ExpirationDate, nil
			}
		}
	}
	twig.Debug("did not get a new link")
	return "", time.Now(), errors.Errorf("couldn't get new signed url for:%s", errfmtstr, inode.Acc, inode.Name)
}

func (fh *FileHandle) resetToKnownSize() {
	if fh.inode.KnownSize != nil {
		fh.inode.Attributes.Size = *fh.inode.KnownSize
	} else {
		fh.inode.Attributes.Size = 0
		fh.inode.Invalid = true
	}
}
