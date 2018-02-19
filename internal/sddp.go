// Copyright 2015 - 2017 Ka-Hing Cheung
// Copyright 2015 - 2017 Google Inc. All Rights Reserved.
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

package internal

import (
	"fmt"
	"mime"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/corehandlers"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/kahing/goofys/nr"
	"github.com/sirupsen/logrus"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

func SDDP_Mount(ctx context.Context, flags *FlagStorage) (*SDDP, *fuse.MountedFileSystem, error) {
	awsConfig := &aws.Config{
		Region:           &flags.Region,
		Logger:           GetLogger("s3"),
		S3ForcePathStyle: aws.Bool(true),
	}
	fmt.Println("about to call NewSDDP")
	fs := NewSDDP(ctx, awsConfig, flags)
	fmt.Println("out of NewSDDP")
	if fs == nil {
		return nil, nil, fmt.Errorf("Mount: initialization failed")
	}
	s := fuseutil.NewFileSystemServer(fs)
	fuseLog := GetLogger("fuse")
	mntConfig := &fuse.MountConfig{
		FSName:                  "sddp",
		ErrorLogger:             GetStdLogger(NewLogger("fuse"), logrus.ErrorLevel),
		DisableWritebackCaching: true,
	}
	if flags.DebugFuse {
		fuseLog.Level = logrus.DebugLevel
		log.Level = logrus.DebugLevel
		mntConfig.DebugLogger = GetStdLogger(fuseLog, logrus.DebugLevel)
	}
	mfs, err := fuse.Mount(flags.MountPoint, s, mntConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("Mount: %v", err)
	}
	return fs, mfs, nil
}

func NewSDDP(ctx context.Context, awsConfig *aws.Config, flags *FlagStorage) *SDDP {
	payload := nr.ResolveNames(flags.Loc, flags.Ncg, flags.Acc)
	bucket := "1000genomes"
	fmt.Println("got bucket name: ", bucket)
	fs := &SDDP{
		bucket: bucket,
		accs:   payload,
		flags:  flags,
		umask:  0122,
	}

	if flags.DebugS3 {
		awsConfig.LogLevel = aws.LogLevel(aws.LogDebug | aws.LogDebugWithRequestErrors)
		s3Log.Level = logrus.DebugLevel
	}

	// TODO: add aws connection back in... maybe... unless I find something else out
	fs.awsConfig = awsConfig
	fs.sess = session.New(awsConfig)
	fs.s3 = fs.newS3()

	// We no longer want to immediately start messing with buckets
	// var isAws bool
	// var err error
	// if !fs.flags.RegionSet {
	// 	fmt.Println("resolving region")
	// 	err, isAws = fs.detectBucketLocationByHEAD()
	// 	if err == nil {
	// 		// we detected a region header, this is probably AWS S3,
	// 		// or we can use anonymous access, or both
	// 		fs.sess = session.New(awsConfig)
	// 		fs.s3 = fs.newS3()
	// 	} else if err == fuse.ENOENT {
	// 		log.Errorf("bucket %v does not exist", fs.bucket)
	// 		return nil
	// 	} else {
	// 		// this is NOT AWS, we expect the request to fail with 403 if this is not
	// 		// an anonymous bucket
	// 		if err != syscall.EACCES {
	// 			log.Errorf("Unable to access '%v': %v", fs.bucket, err)
	// 		}
	// 	}
	// }

	// // try again with the credential to make sure
	// err = mapAwsError(fs.testBucket())
	// if err != nil {
	// 	if !isAws {
	// 		// EMC returns 403 because it doesn't support v4 signing
	// 		// swift3, ceph-s3 returns 400
	// 		// Amplidata just gives up and return 500
	// 		if err == syscall.EACCES || err == fuse.EINVAL || err == syscall.EAGAIN {
	// 			fs.fallbackV2Signer()
	// 			err = mapAwsError(fs.testBucket())
	// 		}
	// 	}

	// 	if err != nil {
	// 		log.Errorf("Unable to access '%v': %v", fs.bucket, err)
	// 		return nil
	// 	}
	// }

	// go fs.cleanUpOldMPU()

	// if flags.UseKMS {
	// 	//SSE header string for KMS server-side encryption (SSE-KMS)
	// 	fs.sseType = s3.ServerSideEncryptionAwsKms
	// } else if flags.UseSSE {
	// 	//SSE header string for non-KMS server-side encryption (SSE-S3)
	// 	fs.sseType = s3.ServerSideEncryptionAes256
	// }

	now := time.Now()
	fs.rootAttrs = SDDP_InodeAttributes{
		Size:  4096,
		Mtime: now,
	}

	// Just taken from NewGoofys... don't know what its purpose is.
	fs.bufferPool = BufferPool{}.Init()

	fs.nextInodeID = fuseops.RootInodeID + 1
	fs.inodes = make(map[fuseops.InodeID]*SDDP_Inode)
	root := SDDP_NewInode(fs, nil, aws.String(""), aws.String(""))
	root.Id = fuseops.RootInodeID
	root.ToDir()
	root.Attributes.Mtime = fs.rootAttrs.Mtime

	fs.inodes[fuseops.RootInodeID] = root

	fs.nextHandleID = 1
	fs.dirHandles = make(map[fuseops.HandleID]*SDDP_DirHandle)

	fs.fileHandles = make(map[fuseops.HandleID]*SDDP_FileHandle)

	fs.replicators = Ticket{Total: 16}.Init()
	fs.restorers = Ticket{Total: 8}.Init()

	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 1000

	for i := range payload {
		// make directories here
		// dir
		fmt.Println("making dir: ", payload[i].ID)
		fullDirName := root.getChildName(payload[i].ID)
		root.mu.Lock()
		dir := SDDP_NewInode(fs, root, &payload[i].ID, &fullDirName)
		dir.ToDir()
		dir.touch()
		root.mu.Unlock()
		fs.mu.Lock()
		fs.insertInode(root, dir)
		fs.mu.Unlock()
		// maybe do this?
		// dir.addDotAndDotDot()
		// put some files in the dirs
		for j := range payload[i].Files {
			fmt.Println("making file: ", payload[i].Files[j].Name)
			fullFileName := dir.getChildName(payload[i].Files[j].Name)
			dir.mu.Lock()
			file := SDDP_NewInode(fs, dir, &payload[i].Files[j].Name, &fullFileName)
			// TODO: This will have to change when the real API is made
			file.Bucket = "1000genomes"
			file.CloudName = "phase3/data/NA19036/alignment/" + payload[i].Files[j].Name
			file.Link = payload[i].Files[j].Link
			u, err := strconv.ParseUint(payload[i].Files[j].Size, 10, 64)
			if err != nil {
				panic("failed to parse size into a uint64")
			}
			file.Attributes = SDDP_InodeAttributes{
				Size:  u,
				Mtime: time.Now(),
			}
			fh := SDDP_NewFileHandle(file)
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
	}

	return fs
}

type SDDP struct {
	fuseutil.NotImplementedFileSystem
	bucket string
	prefix string

	// SDDP specific info
	accs []nr.Accession

	flags *FlagStorage

	umask uint32

	awsConfig *aws.Config
	sess      *session.Session
	s3        *s3.S3
	v2Signer  bool
	sseType   string
	rootAttrs SDDP_InodeAttributes

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
	inodes map[fuseops.InodeID]*SDDP_Inode

	nextHandleID fuseops.HandleID
	dirHandles   map[fuseops.HandleID]*SDDP_DirHandle

	fileHandles map[fuseops.HandleID]*SDDP_FileHandle

	replicators *Ticket
	restorers   *Ticket

	forgotCnt uint32
}

func (fs *SDDP) allocateInodeId() (id fuseops.InodeID) {
	id = fs.nextInodeID
	fs.nextInodeID++
	return
}

// Make an AWS key by using the prefix given initially... may not be needed for SDDP usage
func (fs *SDDP) key(name string) *string {
	// name = fs.prefix + name
	return &name
}

func (fs *SDDP) getMimeType(fileName string) (retMime *string) {
	fmt.Println("sddp.go/getMimeType called")
	if fs.flags.UseContentType {
		dotPosition := strings.LastIndex(fileName, ".")
		if dotPosition == -1 {
			return nil
		}
		mimeType := mime.TypeByExtension(fileName[dotPosition:])
		if mimeType == "" {
			return nil
		}
		semicolonPosition := strings.LastIndex(mimeType, ";")
		if semicolonPosition == -1 {
			return &mimeType
		}
		retMime = aws.String(mimeType[:semicolonPosition])
	}

	return
}

func (fs *SDDP) SigUsr1() {
	fs.mu.Lock()

	log.Infof("forgot %v inodes", fs.forgotCnt)
	log.Infof("%v inodes", len(fs.inodes))
	fs.mu.Unlock()
	debug.FreeOSMemory()
}

func (fs *SDDP) detectBucketLocationByHEAD() (err error, isAws bool) {
	u := url.URL{
		Scheme: "https",
		Host:   "s3.amazonaws.com",
		Path:   fs.bucket,
	}

	if fs.awsConfig.Endpoint != nil {
		endpoint, err := url.Parse(*fs.awsConfig.Endpoint)
		if err != nil {
			return err, false
		}

		u.Scheme = endpoint.Scheme
		u.Host = endpoint.Host
	}

	var req *http.Request
	var resp *http.Response

	req, err = http.NewRequest("HEAD", u.String(), nil)
	if err != nil {
		return
	}

	allowFails := 3
	for i := 0; i < allowFails; i++ {
		resp, err = http.DefaultTransport.RoundTrip(req)
		if err != nil {
			return
		}
		if resp.StatusCode < 500 {
			break
		} else if resp.StatusCode == 503 && resp.Status == "503 Slow Down" {
			time.Sleep(time.Duration(i+1) * time.Second)
			// allow infinite retries for 503 slow down
			allowFails += 1
		}
	}

	region := resp.Header["X-Amz-Bucket-Region"]
	server := resp.Header["Server"]

	s3Log.Debugf("HEAD %v = %v %v", u.String(), resp.StatusCode, region)
	if region == nil {
		for k, v := range resp.Header {
			s3Log.Debugf("%v = %v", k, v)
		}
	}
	if server != nil && server[0] == "AmazonS3" {
		isAws = true
	}

	switch resp.StatusCode {
	case 200:
		// note that this only happen if the bucket is in us-east-1
		if len(fs.flags.Profile) == 0 {
			fs.awsConfig.Credentials = credentials.AnonymousCredentials
			s3Log.Infof("anonymous bucket detected")
		}
	case 400:
		err = fuse.EINVAL
	case 403:
		err = syscall.EACCES
	case 404:
		err = fuse.ENOENT
	case 405:
		err = syscall.ENOTSUP
	default:
		err = awserr.New(strconv.Itoa(resp.StatusCode), resp.Status, nil)
	}

	if len(region) != 0 {
		if region[0] != *fs.awsConfig.Region {
			s3Log.Infof("Switching from region '%v' to '%v'",
				*fs.awsConfig.Region, region[0])
			fs.awsConfig.Region = &region[0]
		}

		// we detected a region, this is aws, the error is irrelevant
		err = nil
	}

	return
}

// Find the given inode. Panic if it doesn't exist.
//
// LOCKS_REQUIRED(fs.mu)
func (fs *SDDP) getInodeOrDie(id fuseops.InodeID) (inode *SDDP_Inode) {
	inode = fs.inodes[id]
	if inode == nil {
		panic(fmt.Sprintf("Unknown inode: %v", id))
	}

	return
}

func (fs *SDDP) StatFS(ctx context.Context, op *fuseops.StatFSOp) (err error) {
	fmt.Println("sddp.go/StatFS called")

	const BLOCK_SIZE = 4096
	const TOTAL_SPACE = 1 * 1024 * 1024 * 1024 * 1024 * 1024 // 1PB
	const TOTAL_BLOCKS = TOTAL_SPACE / BLOCK_SIZE
	const INODES = 1 * 1000 * 1000 * 1000 // 1 billion
	op.BlockSize = BLOCK_SIZE
	op.Blocks = TOTAL_BLOCKS
	op.BlocksFree = TOTAL_BLOCKS
	op.BlocksAvailable = TOTAL_BLOCKS
	op.IoSize = 1 * 1024 * 1024 // 1MB
	op.Inodes = INODES
	op.InodesFree = INODES
	return
}

func (fs *SDDP) GetInodeAttributes(ctx context.Context, op *fuseops.GetInodeAttributesOp) (err error) {
	fmt.Println("sddp.go/GetInodeAttributes called")

	fs.mu.Lock()
	inode := fs.getInodeOrDie(op.Inode)
	fs.mu.Unlock()

	attr, err := inode.GetAttributes()
	if err == nil {
		op.Attributes = *attr
		op.AttributesExpiration = time.Now().Add(fs.flags.StatCacheTTL)
	}

	return
}

func (fs *SDDP) GetXattr(ctx context.Context, op *fuseops.GetXattrOp) (err error) {
	fmt.Println("sddp.go/GetXattr called")
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
	} else {
		copy(op.Dst, value)
		return
	}
}

func (fs *SDDP) ListXattr(ctx context.Context, op *fuseops.ListXattrOp) (err error) {
	fmt.Println("sddp.go/ListXattr called")
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

func (fs *SDDP) LookUpInode(ctx context.Context, op *fuseops.LookUpInodeOp) (err error) {
	fmt.Println("sddp.go/LookUpInode called with:")
	fmt.Println("op.Parent: ", op.Parent)
	fmt.Println("op.Name: ", op.Name)

	var inode *SDDP_Inode
	var ok bool
	defer func() { fuseLog.Debugf("<-- LookUpInode %v %v %v", op.Parent, op.Name, err) }()

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

	op.Entry.Child = inode.Id
	op.Entry.Attributes = inode.InflateAttributes()
	op.Entry.AttributesExpiration = time.Now().Add(fs.flags.StatCacheTTL)
	op.Entry.EntryExpiration = time.Now().Add(fs.flags.TypeCacheTTL)

	return
}

// LOCKS_REQUIRED(fs.mu)
// LOCKS_REQUIRED(parent.mu)
func (fs *SDDP) insertInode(parent *SDDP_Inode, inode *SDDP_Inode) {
	fmt.Println("sddp.go/insertInode called")
	inode.Id = fs.allocateInodeId()
	parent.insertChildUnlocked(inode)
	fs.inodes[inode.Id] = inode
}

func (fs *SDDP) OpenDir(ctx context.Context, op *fuseops.OpenDirOp) (err error) {
	fmt.Println("sddp.go/OpenDir called with")
	fmt.Println("op.Inode: ", op.Inode)
	fs.mu.Lock()

	handleID := fs.nextHandleID
	fs.nextHandleID++

	in := fs.getInodeOrDie(op.Inode)
	fs.mu.Unlock()

	// XXX/is this a dir?
	dh := in.OpenDir()

	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.dirHandles[handleID] = dh
	op.Handle = handleID

	return
}

// LOCKS_EXCLUDED(fs.mu)
func (fs *SDDP) insertInodeFromDirEntry(parent *SDDP_Inode, entry *SDDP_DirHandleEntry) (inode *SDDP_Inode) {
	fmt.Println("sddp.go/insertInodeFromDirEntry called")
	parent.mu.Lock()
	defer parent.mu.Unlock()

	inode = parent.findChildUnlocked(*entry.Name, entry.Type == fuseutil.DT_Directory)
	if inode == nil {
		path := parent.getChildName(*entry.Name)
		inode = SDDP_NewInode(fs, parent, entry.Name, &path)
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

func SDDP_makeDirEntry(en *SDDP_DirHandleEntry) fuseutil.Dirent {
	fmt.Println("sddp.go/makeDirEntry called with")
	fmt.Println("en.Name: ", *en.Name)
	fmt.Println("en.Type: ", en.Type)
	fmt.Println("en.Offset: ", en.Offset)
	return fuseutil.Dirent{
		Name:   *en.Name,
		Type:   en.Type,
		Inode:  fuseops.RootInodeID + 1,
		Offset: en.Offset,
	}
}

// LOCKS_EXCLUDED(fs.mu)
func (fs *SDDP) ReadDir(ctx context.Context, op *fuseops.ReadDirOp) (err error) {
	fmt.Println("sddp.go/ReadDir called with")
	fmt.Println("op.Handle: ", op.Handle)

	// Find the handle.
	fs.mu.Lock()
	dh := fs.dirHandles[op.Handle]
	fs.mu.Unlock()

	if dh == nil {
		panic(fmt.Sprintf("can't find dh=%v", op.Handle))
	}

	inode := dh.inode
	inode.logFuse("ReadDir", op.Offset)

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

		n := fuseutil.WriteDirent(op.Dst[op.BytesRead:], SDDP_makeDirEntry(e))
		if n == 0 {
			break
		}

		dh.inode.logFuse("<-- ReadDir", *e.Name, e.Offset)

		op.BytesRead += n
	}

	return
}

func (fs *SDDP) ReleaseDirHandle(ctx context.Context, op *fuseops.ReleaseDirHandleOp) (err error) {
	fmt.Println("sddp.go/ReleaseDirHandle called")

	fs.mu.Lock()
	defer fs.mu.Unlock()

	dh := fs.dirHandles[op.Handle]
	dh.CloseDir()

	fuseLog.Debugln("ReleaseDirHandle", *dh.inode.FullName())

	delete(fs.dirHandles, op.Handle)

	return
}

func (fs *SDDP) OpenFile(ctx context.Context, op *fuseops.OpenFileOp) (err error) {
	fmt.Println("sddp.go/OpenFile called")
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

func (fs *SDDP) ReadFile(ctx context.Context, op *fuseops.ReadFileOp) (err error) {
	fmt.Println("sddp.go/ReadFile called")

	fs.mu.Lock()
	fh := fs.fileHandles[op.Handle]
	fs.mu.Unlock()

	op.BytesRead, err = fh.ReadFile(op.Offset, op.Dst)

	return
}

func (fs *SDDP) SyncFile(ctx context.Context, op *fuseops.SyncFileOp) (err error) {

	// intentionally ignored, so that write()/sync()/write() works
	// see https://github.com/kahing/goofys/issues/154
	return
}

func (fs *SDDP) FlushFile(ctx context.Context, op *fuseops.FlushFileOp) (err error) {
	fmt.Println("sddp.go/FlushFile called")

	fs.mu.Lock()
	fh := fs.fileHandles[op.Handle]
	fs.mu.Unlock()

	err = fh.FlushFile()
	if err != nil {
		// if we returned success from creat() earlier
		// linux may think this file exists even when it doesn't,
		// until TypeCacheTTL is over
		// TODO: figure out a way to make the kernel forget this inode
		// see TestWriteAnonymousFuse
		fs.mu.Lock()
		inode := fs.getInodeOrDie(op.Inode)
		fs.mu.Unlock()

		if inode.KnownSize == nil {
			inode.AttrTime = time.Time{}
		}

	}
	fh.inode.logFuse("<-- FlushFile", err)

	return
}

func (fs *SDDP) ReleaseFileHandle(ctx context.Context, op *fuseops.ReleaseFileHandleOp) (err error) {
	fmt.Println("sddp.go/ReleaseFileHandle called")
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fh := fs.fileHandles[op.Handle]
	fh.Release()

	fuseLog.Debugln("ReleaseFileHandle", *fh.inode.FullName())

	delete(fs.fileHandles, op.Handle)

	// try to compact heap
	//fs.bufferPool.MaybeGC()
	return
}

func (fs *SDDP) newS3() *s3.S3 {
	svc := s3.New(fs.sess)
	if fs.v2Signer {
		svc.Handlers.Sign.Clear()
		svc.Handlers.Sign.PushBack(SignV2)
		svc.Handlers.Sign.PushBackNamed(corehandlers.BuildContentLengthHandler)
	}
	svc.Handlers.Sign.PushBack(addAcceptEncoding)
	return svc
}

func (fs *SDDP) cleanUpOldMPU() {
	mpu, err := fs.s3.ListMultipartUploads(&s3.ListMultipartUploadsInput{Bucket: &fs.bucket})
	if err != nil {
		mapAwsError(err)
		return
	}
	s3Log.Debug(mpu)

	now := time.Now()
	for _, upload := range mpu.Uploads {
		expireTime := upload.Initiated.Add(48 * time.Hour)

		if !expireTime.After(now) {
			params := &s3.AbortMultipartUploadInput{
				Bucket:   &fs.bucket,
				Key:      upload.Key,
				UploadId: upload.UploadId,
			}
			resp, err := fs.s3.AbortMultipartUpload(params)
			s3Log.Debug(resp)

			if mapAwsError(err) == syscall.EACCES {
				break
			}
		} else {
			s3Log.Debugf("Keeping MPU Key=%v Id=%v", *upload.Key, *upload.UploadId)
		}
	}
}

func (fs *SDDP) fallbackV2Signer() (err error) {
	if fs.v2Signer {
		return fuse.EINVAL
	}

	s3Log.Infoln("Falling back to v2 signer")
	fs.v2Signer = true
	fs.s3 = fs.newS3()
	return
}

func (fs *SDDP) testBucket() (err error) {
	randomObjectName := fs.key(RandStringBytesMaskImprSrc(32))

	_, err = fs.s3.HeadObject(&s3.HeadObjectInput{Bucket: &fs.bucket, Key: randomObjectName})
	if err != nil {
		err = mapAwsError(err)
		if err == fuse.ENOENT {
			err = nil
		}
	}

	return
}

// func (fs *Goofys) mkSRRs(accs []string) error {
// 	fs.mu.Lock()
// 	parent := fs.getInodeOrDie(fuseops.RootInodeID)
// 	fs.mu.Unlock()
// 	for _, acc := range accs {
// 		err := fs.mkdir(parent, acc)
// 		if err != nil {
// 			return err
// 		}
// 	}
// 	return nil
// }

// func (fs *Goofys) mkdir(parent *Inode, name string) error {
// 	// ignore op.Mode for now
// 	inode, err := parent.MkDir(name)
// 	if err != nil {
// 		return err
// 	}

// 	fs.mu.Lock()
// 	defer fs.mu.Unlock()

// 	fs.insertInode(parent, inode)

// 	return nil
// }
