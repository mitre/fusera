package internal

import (
	"os"
	"time"
)

type Config struct {
	// SDDP flags
	Ncg string
	Acc []string
	Loc string

	// File system
	MountOptions map[string]string
	MountPoint   string

	Cache    []string
	DirMode  os.FileMode
	FileMode os.FileMode
	Uid      uint32
	Gid      uint32

	// S3
	Endpoint       string
	Region         string
	RegionSet      bool
	StorageClass   string
	Profile        string
	UseContentType bool
	UseSSE         bool
	UseKMS         bool
	KMSKeyID       string
	ACL            string

	// Tuning
	Cheap        bool
	ExplicitDir  bool
	StatCacheTTL time.Duration
	TypeCacheTTL time.Duration

	// Debugging
	DebugFuse  bool
	DebugS3    bool
	Foreground bool
}
