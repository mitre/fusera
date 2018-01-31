package internal

import (
	"fmt"
	"mime"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/context"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
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
	// bucket := getBucketName(resolveNames(flags.Loc, flags.Ncg, flags.Acc))
	// fmt.Println("got bucket name: ", bucket)
	payload := resolveNames(flags.Loc, flags.Ncg, flags.Acc)
	fs := &SDDP{
		// bucket: bucket,
		accs:  payload.Accessions,
		flags: flags,
		umask: 0122,
	}

	if flags.DebugS3 {
		awsConfig.LogLevel = aws.LogLevel(aws.LogDebug | aws.LogDebugWithRequestErrors)
		s3Log.Level = logrus.DebugLevel
	}

	// TODO: add aws connection back in... maybe... unless I find something else out
	// fs.awsConfig = awsConfig
	// fs.sess = session.New(awsConfig)
	// fs.s3 = fs.newS3()

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

	// try again with the credential to make sure
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

	if flags.UseKMS {
		//SSE header string for KMS server-side encryption (SSE-KMS)
		fs.sseType = s3.ServerSideEncryptionAwsKms
	} else if flags.UseSSE {
		//SSE header string for non-KMS server-side encryption (SSE-S3)
		fs.sseType = s3.ServerSideEncryptionAes256
	}

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
	fs.dirHandles = make(map[fuseops.HandleID]*DirHandle)

	fs.fileHandles = make(map[fuseops.HandleID]*FileHandle)

	fs.replicators = Ticket{Total: 16}.Init()
	fs.restorers = Ticket{Total: 8}.Init()

	http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost = 1000
	// Use Goofys functions to create initial directory structure.
	//fs.mkSRRs(flags.Acc)

	return fs
}

type SDDP struct {
	fuseutil.NotImplementedFileSystem
	// bucket string
	// prefix string

	// SDDP specific info
	accs []Accession

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
	dirHandles   map[fuseops.HandleID]*DirHandle

	fileHandles map[fuseops.HandleID]*FileHandle

	replicators *Ticket
	restorers   *Ticket

	forgotCnt uint32
}

// LOCKS_EXCLUDED(fs.mu)
func (fs *SDDP) insertInodeFromDirEntry(parent *SDDP_Inode, entry *SDDP_DirHandleEntry) (inode *SDDP_Inode) {
	fmt.Println("goofys.go/insertInodeFromDirEntry called")
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

// LOCKS_REQUIRED(fs.mu)
// LOCKS_REQUIRED(parent.mu)
func (fs *SDDP) insertInode(parent *SDDP_Inode, inode *SDDP_Inode) {
	fmt.Println("goofys.go/insertInode called")
	inode.Id = fs.allocateInodeId()
	parent.insertChildUnlocked(inode)
	fs.inodes[inode.Id] = inode
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
	fmt.Println("goofys.go/getMimeType called")
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
