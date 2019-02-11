// Copyright 2015 - 2017 Ka-Hing Cheung
// Copyright 2015 - 2017 Google Inc. All Rights Reserved.
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
	"context"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mitre/fusera/awsutil"
	"github.com/pkg/errors"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

// Options is a collection of values that describe how Fusera should behave.
type Options struct {
	// The file used to authenticate with the SRA Data Locator API
	API API
	Acc []*Accession
	//Region   string
	Platform *awsutil.Platform
	Profile  string

	// File system
	MountOptions      map[string]string
	MountPoint        string
	MountPointArg     string
	MountPointCreated string

	Cache []string
	UID   uint32
	GID   uint32

	// // Debugging
	Debug bool
}

// Mount the file system based on the supplied arguments, returning a
// fuse.MountedFileSystem that can be joined to wait for unmounting.
func Mount(ctx context.Context, opt *Options) (*Fusera, *fuse.MountedFileSystem, error) {
	fs, err := NewFusera(ctx, opt)
	if err != nil {
		return nil, nil, err
	}
	if fs == nil {
		return nil, nil, errors.New("failure to mount: initialization failed")
	}
	s := fuseutil.NewFileSystemServer(fs)
	mntConfig := &fuse.MountConfig{
		FSName:                  "fusera",
		DisableWritebackCaching: true,
	}
	mfs, err := fuse.Mount(opt.MountPoint, s, mntConfig)
	if err != nil {
		return nil, nil, errors.Wrap(err, "failure to mount")
	}
	return fs, mfs, nil
}

func NewFusera(ctx context.Context, opt *Options) (*Fusera, error) {
	fs := &Fusera{
		signer:   opt.API,
		accs:     opt.Acc,
		opt:      opt,
		DirMode:  0555,
		FileMode: 0444,
		umask:    0122,
	}

	now := time.Now()
	fs.rootAttrs = InodeAttributes{
		Size:  4096,
		Mtime: now,
	}

	fs.bufferPool = BufferPool{}.Init()

	fs.nextInodeID = fuseops.RootInodeID + 1
	fs.inodes = make(map[fuseops.InodeID]*Inode)
	root := NewInode(fs, nil, awsutil.String(""), awsutil.String(""))
	root.ID = fuseops.RootInodeID
	root.ToDir()
	root.Attributes.Mtime = fs.rootAttrs.Mtime

	fs.inodes[fuseops.RootInodeID] = root

	fs.nextHandleID = 1
	fs.dirHandles = make(map[fuseops.HandleID]*DirHandle)

	fs.fileHandles = make(map[fuseops.HandleID]*FileHandle)

	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 1000

	for _, acc := range fs.accs {
		// make directories here
		// dir
		//fmt.Println("making dir: ", accessions[i].ID)
		fullDirName := root.getChildName(acc.ID)
		root.mu.Lock()
		dir := NewInode(fs, root, awsutil.String(acc.ID), &fullDirName)
		dir.ToDir()
		dir.touch()
		root.mu.Unlock()
		fs.mu.Lock()
		fs.insertInode(root, dir)
		fs.mu.Unlock()
		// maybe do this?
		// dir.addDotAndDotDot()
		// put some files in the dirs
		for name, f := range acc.Files {
			fullFileName := dir.getChildName(name)
			dir.mu.Lock()
			file := NewInode(fs, dir, awsutil.String(name), &fullFileName)
			file.Link = f.Link
			if f.Bucket != "" {
				file.ReqPays = true
				file.Bucket = f.Bucket
				file.Key = f.Key
				file.Platform = opt.Platform
			}
			file.Acc = acc.ID
			u, err := strconv.ParseUint(f.Size, 10, 64)
			if err != nil {
				// twig.Debug("%s: %s: failed to set file size to %s, couldn't parse into a uint64", acc.ID, file.Name, f.Size)
				u = 0
			}
			file.Attributes = InodeAttributes{
				Size:           u,
				Mtime:          f.ModifiedDate,
				ExpirationDate: f.ExpirationDate,
			}

			fh := NewFileHandle(file)
			fh.poolHandle = fs.bufferPool
			fh.buf = MBuf{}.Init(fh.poolHandle, 0, true)
			fh.dirty = true
			file.fileHandles = 1
			dir.touch()
			dir.mu.Unlock()
			fs.mu.Lock()
			// dir.insertChild(file)
			fs.insertInode(dir, file)
			hID := fs.nextHandleID
			fs.nextHandleID++
			fs.fileHandles[hID] = fh
			fs.mu.Unlock()

			// 	children: []fuseutil.Dirent{
			// 		fuseutil.Dirent{
			// 			Offset: 1,
			// 			Inode:  worldInode,
			// 			Name:   "world",
			// 			Type:   fuseutil.DT_File,
			// 		},
			// 	},
			// }
		}
		// twig.Debugf("accession's err content: %s", acc.ErrorLog())
		if acc.HasError() {
			// twig.Debugf("accession: %s has an error file", acc.ID)
			errlogName := "error.log"
			fullFileName := dir.getChildName(errlogName)
			dir.mu.Lock()
			file := NewInode(fs, dir, awsutil.String(errlogName), &fullFileName)
			file.Acc = acc.ID
			file.ErrContents = acc.ErrorLog()
			file.Attributes = InodeAttributes{
				Size:           uint64(len(acc.ErrorLog())),
				Mtime:          time.Now(),
				ExpirationDate: time.Now(),
			}

			fh := NewFileHandle(file)
			fh.poolHandle = fs.bufferPool
			fh.buf = MBuf{}.Init(fh.poolHandle, 0, true)
			fh.dirty = true
			file.fileHandles = 1
			dir.touch()
			dir.mu.Unlock()
			fs.mu.Lock()
			// dir.insertChild(file)
			fs.insertInode(dir, file)
			hID := fs.nextHandleID
			fs.nextHandleID++
			fs.fileHandles[hID] = fh
			fs.mu.Unlock()
		}
	}
	name := ".initialized"
	fullName := root.getChildName(name)
	root.mu.Lock()
	node := NewInode(fs, root, awsutil.String(name), &fullName)
	node.Attributes = InodeAttributes{
		Size: 0,
	}
	fh := NewFileHandle(node)
	fh.poolHandle = fs.bufferPool
	fh.buf = MBuf{}.Init(fh.poolHandle, 0, true)
	fh.dirty = true
	node.fileHandles = 1
	root.touch()
	root.mu.Unlock()
	fs.mu.Lock()
	fs.insertInode(root, node)
	hID := fs.nextHandleID
	fs.nextHandleID++
	fs.fileHandles[hID] = fh
	fs.mu.Unlock()

	return fs, nil
}

type Fusera struct {
	fuseutil.NotImplementedFileSystem

	// Fusera specific info
	accs   []*Accession
	opt    *Options
	signer API
	umask  uint32

	DirMode    os.FileMode
	FileMode   os.FileMode
	rootAttrs  InodeAttributes
	bufferPool *BufferPool

	// A lock protecting the state of the file system struct itself (distinct
	// from per-inode locks). Make sure to see the notes on lock ordering above.
	mu sync.Mutex

	// The next inode ID to hand out. We assume that this will never overflow,
	// since even if we were handing out inode IDs at 4 GHz, it would still take
	// over a century to do so.
	//
	// GUARDED_BY(mu)
	nextInodeID fuseops.InodeID

	// The collection of live inodes, keyed by inode ID. No ID less than
	// fuseops.RootInodeID is ever used.
	//
	// INVARIANT: For all keys k, fuseops.RootInodeID <= k < nextInodeID
	// INVARIANT: For all keys k, inodes[k].ID() == k
	// INVARIANT: inodes[fuseops.RootInodeID] is missing or of type inode.DirInode
	// INVARIANT: For all v, if IsDirName(v.Name()) then v is inode.DirInode
	//
	// GUARDED_BY(mu)
	inodes       map[fuseops.InodeID]*Inode
	nextHandleID fuseops.HandleID
	dirHandles   map[fuseops.HandleID]*DirHandle
	fileHandles  map[fuseops.HandleID]*FileHandle
	forgotCnt    uint32
}

func (fs *Fusera) allocateInodeID() (id fuseops.InodeID) {
	id = fs.nextInodeID
	fs.nextInodeID++
	return
}

func (fs *Fusera) SigUsr1() {
	fs.mu.Lock()

	// twig.Infof("forgot %v inodes", fs.forgotCnt)
	// twig.Infof("%v inodes", len(fs.inodes))
	fs.mu.Unlock()
	debug.FreeOSMemory()
}

// Find the given inode. Panic if it doesn't exist.
//
// LOCKS_REQUIRED(fs.mu)
func (fs *Fusera) getInodeOrDie(id fuseops.InodeID) (inode *Inode) {
	inode = fs.inodes[id]
	if inode == nil {
		panic(fmt.Sprintf("Unknown inode: %v", id))
	}

	return
}

func (fs *Fusera) StatFS(ctx context.Context, op *fuseops.StatFSOp) (err error) {
	var totalSpace uint64
	for _, a := range fs.accs {
		for _, f := range a.Files {
			s, err := strconv.ParseUint(f.Size, 10, 64)
			if err != nil {
				totalSpace = 1024 * 1024 * 1024
				goto skip
			}
			totalSpace += s
		}
	}
skip:
	const blockSize = 4096
	totalBlocks := totalSpace / blockSize
	const INODES = 1 * 1000 * 1000 * 1000 // 1 billion
	op.BlockSize = blockSize
	op.Blocks = totalBlocks
	op.BlocksFree = 0
	op.BlocksAvailable = 0
	op.IoSize = 1 * 1024 * 1024 // 1MB
	op.Inodes = INODES
	op.InodesFree = 0
	return
}

func (fs *Fusera) GetInodeAttributes(ctx context.Context, op *fuseops.GetInodeAttributesOp) (err error) {
	fs.mu.Lock()
	inode := fs.getInodeOrDie(op.Inode)
	fs.mu.Unlock()

	attr, err := inode.GetAttributes()
	if err == nil {
		op.Attributes = *attr
	}

	return
}

func (fs *Fusera) GetXattr(ctx context.Context, op *fuseops.GetXattrOp) (err error) {
	fs.mu.Lock()
	inode := fs.getInodeOrDie(op.Inode)
	fs.mu.Unlock()

	value, err := inode.GetXattr(op.Name)
	if err != nil {
		return
	}

	op.BytesRead = len(value)

	if len(op.Dst) < op.BytesRead {
		return syscall.ERANGE
	}
	copy(op.Dst, value)
	return
}

func (fs *Fusera) ListXattr(ctx context.Context, op *fuseops.ListXattrOp) (err error) {
	fs.mu.Lock()
	inode := fs.getInodeOrDie(op.Inode)
	fs.mu.Unlock()

	xattrs, err := inode.ListXattr()
	ncopied := 0
	for _, name := range xattrs {
		buf := op.Dst[ncopied:]
		nlen := len(name) + 1

		if nlen <= len(buf) {
			copy(buf, name)
			ncopied += nlen
			buf[nlen-1] = '\x00'
		}

		op.BytesRead += nlen
	}

	if ncopied < op.BytesRead {
		err = syscall.ERANGE
	}

	return
}

func (fs *Fusera) LookUpInode(ctx context.Context, op *fuseops.LookUpInodeOp) (err error) {
	var inode *Inode
	var ok bool

	fs.mu.Lock()
	parent := fs.getInodeOrDie(op.Parent)
	fs.mu.Unlock()

	parent.mu.Lock()
	fs.mu.Lock()
	inode = parent.findChildUnlockedFull(op.Name)
	if inode != nil {
		ok = true
		inode.Ref()
	} else {
		ok = false
	}
	fs.mu.Unlock()
	parent.mu.Unlock()

	if !ok {
		return fuse.ENOENT
	}

	op.Entry.Child = inode.ID
	op.Entry.Attributes = inode.InflateAttributes()

	return
}

// LOCKS_REQUIRED(fs.mu)
// LOCKS_REQUIRED(parent.mu)
func (fs *Fusera) insertInode(parent *Inode, inode *Inode) {
	inode.ID = fs.allocateInodeID()
	parent.insertChildUnlocked(inode)
	fs.inodes[inode.ID] = inode
}

func (fs *Fusera) OpenDir(ctx context.Context, op *fuseops.OpenDirOp) (err error) {
	fs.mu.Lock()
	handleID := fs.nextHandleID
	fs.nextHandleID++
	in := fs.getInodeOrDie(op.Inode)
	fs.mu.Unlock()

	dh := in.OpenDir()

	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.dirHandles[handleID] = dh
	op.Handle = handleID

	return
}

// LOCKS_EXCLUDED(fs.mu)
func (fs *Fusera) insertInodeFromDirEntry(parent *Inode, entry *DirHandleEntry) (inode *Inode) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	inode = parent.findChildUnlocked(*entry.Name, entry.Type == fuseutil.DT_Directory)
	if inode == nil {
		path := parent.getChildName(*entry.Name)
		inode = NewInode(fs, parent, entry.Name, &path)
		if entry.Type == fuseutil.DT_Directory {
			inode.ToDir()
		} else {
			inode.Attributes = *entry.Attributes
		}
		if entry.ETag != nil {
			inode.s3Metadata["etag"] = []byte(*entry.ETag)
		}
		if entry.StorageClass != nil {
			inode.s3Metadata["storage-class"] = []byte(*entry.StorageClass)
		}
		// these are fake dir entries, we will realize the refcnt when
		// lookup is done
		inode.refcnt = 0

		fs.mu.Lock()
		defer fs.mu.Unlock()

		fs.insertInode(parent, inode)
	} else {
		inode.mu.Lock()
		defer inode.mu.Unlock()

		if entry.ETag != nil {
			inode.s3Metadata["etag"] = []byte(*entry.ETag)
		}
		if entry.StorageClass != nil {
			inode.s3Metadata["storage-class"] = []byte(*entry.StorageClass)
		}
		inode.KnownSize = &entry.Attributes.Size
		inode.Attributes.Mtime = entry.Attributes.Mtime
		inode.AttrTime = time.Now()
	}
	return
}

func makeDirEntry(en *DirHandleEntry) fuseutil.Dirent {
	return fuseutil.Dirent{
		Name:   *en.Name,
		Type:   en.Type,
		Inode:  fuseops.RootInodeID + 1,
		Offset: en.Offset,
	}
}

// LOCKS_EXCLUDED(fs.mu)
func (fs *Fusera) ReadDir(ctx context.Context, op *fuseops.ReadDirOp) (err error) {
	// Find the handle.
	fs.mu.Lock()
	dh := fs.dirHandles[op.Handle]
	fs.mu.Unlock()

	if dh == nil {
		panic(fmt.Sprintf("can't find dh=%v", op.Handle))
	}

	inode := dh.inode

	dh.mu.Lock()
	defer dh.mu.Unlock()

	readFromS3 := false

	for i := op.Offset; ; i++ {
		e, err := dh.ReadDir(i)
		if err != nil {
			return err
		}
		if e == nil {
			// we've reached the end, if this was read
			// from S3 then update the cache time
			if readFromS3 {
				inode.dir.DirTime = time.Now()
				inode.Attributes.Mtime = inode.findChildMaxTime()
			}
			break
		}

		if e.Inode == 0 {
			readFromS3 = true
			fs.insertInodeFromDirEntry(inode, e)
		}

		n := fuseutil.WriteDirent(op.Dst[op.BytesRead:], makeDirEntry(e))
		if n == 0 {
			break
		}

		op.BytesRead += n
	}

	return
}

func (fs *Fusera) ReleaseDirHandle(ctx context.Context, op *fuseops.ReleaseDirHandleOp) (err error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	dh := fs.dirHandles[op.Handle]
	dh.CloseDir()

	delete(fs.dirHandles, op.Handle)

	return
}

func (fs *Fusera) OpenFile(ctx context.Context, op *fuseops.OpenFileOp) (err error) {
	fs.mu.Lock()
	in := fs.getInodeOrDie(op.Inode)
	fs.mu.Unlock()

	fh, err := in.OpenFile()
	if err != nil {
		return
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	handleID := fs.nextHandleID
	fs.nextHandleID++

	fs.fileHandles[handleID] = fh

	op.Handle = handleID
	op.KeepPageCache = true

	return
}

func (fs *Fusera) ReadFile(ctx context.Context, op *fuseops.ReadFileOp) (err error) {
	fs.mu.Lock()
	fh := fs.fileHandles[op.Handle]
	fs.mu.Unlock()
	op.BytesRead, err = fh.ReadFile(op.Offset, op.Dst)
	return
}

func (fs *Fusera) SyncFile(ctx context.Context, op *fuseops.SyncFileOp) (err error) {
	// intentionally ignored, so that write()/sync()/write() works
	// see https://github.com/kahing/goofys/issues/154
	return
}

func (fs *Fusera) ReleaseFileHandle(ctx context.Context, op *fuseops.ReleaseFileHandleOp) (err error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fh := fs.fileHandles[op.Handle]
	fh.Release()

	delete(fs.fileHandles, op.Handle)
	return
}
