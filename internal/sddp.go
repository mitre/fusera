package internal

import (
	"fmt"
	"net/http"
	"syscall"
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

func Mount(ctx context.Context, flags *FlagStorage) (*Goofys, *fuse.MountedFileSystem, error) {
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
		FSName:                  fs.bucket,
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

func NewSDDP(ctx context.Context, awsConfig *aws.Config, flags *FlagStorage) *Goofys {
	bucket := getBucketName(resolveNames(flags.Loc, flags.Ncg, flags.Acc))
	fmt.Println("got bucket name: ", bucket)
	fs := &Goofys{
		bucket: bucket,
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

	var isAws bool
	var err error
	if !fs.flags.RegionSet {
		fmt.Println("resolving region")
		err, isAws = fs.detectBucketLocationByHEAD()
		if err == nil {
			// we detected a region header, this is probably AWS S3,
			// or we can use anonymous access, or both
			fs.sess = session.New(awsConfig)
			fs.s3 = fs.newS3()
		} else if err == fuse.ENOENT {
			log.Errorf("bucket %v does not exist", fs.bucket)
			return nil
		} else {
			// this is NOT AWS, we expect the request to fail with 403 if this is not
			// an anonymous bucket
			if err != syscall.EACCES {
				log.Errorf("Unable to access '%v': %v", fs.bucket, err)
			}
		}
	}

	// try again with the credential to make sure
	err = mapAwsError(fs.testBucket())
	if err != nil {
		if !isAws {
			// EMC returns 403 because it doesn't support v4 signing
			// swift3, ceph-s3 returns 400
			// Amplidata just gives up and return 500
			if err == syscall.EACCES || err == fuse.EINVAL || err == syscall.EAGAIN {
				fs.fallbackV2Signer()
				err = mapAwsError(fs.testBucket())
			}
		}

		if err != nil {
			log.Errorf("Unable to access '%v': %v", fs.bucket, err)
			return nil
		}
	}

	go fs.cleanUpOldMPU()

	if flags.UseKMS {
		//SSE header string for KMS server-side encryption (SSE-KMS)
		fs.sseType = s3.ServerSideEncryptionAwsKms
	} else if flags.UseSSE {
		//SSE header string for non-KMS server-side encryption (SSE-S3)
		fs.sseType = s3.ServerSideEncryptionAes256
	}

	now := time.Now()
	fs.rootAttrs = InodeAttributes{
		Size:  4096,
		Mtime: now,
	}

	// Just taken from NewGoofys... don't know what its purpose is.
	fs.bufferPool = BufferPool{}.Init()

	fs.nextInodeID = fuseops.RootInodeID + 1
	fs.inodes = make(map[fuseops.InodeID]*Inode)
	root := NewInode(fs, nil, aws.String(""), aws.String(""))
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
