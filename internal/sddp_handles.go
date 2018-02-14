// Copyright 2015 - 2017 Ka-Hing Cheung
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
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"

	"github.com/sirupsen/logrus"
)

type SDDP_InodeAttributes struct {
	Size  uint64
	Mtime time.Time
}

type SDDP_Inode struct {
	Id         fuseops.InodeID
	Name       *string
	Bucket     string
	CloudName  string
	Link       string
	fs         *SDDP
	Attributes SDDP_InodeAttributes
	KnownSize  *uint64
	AttrTime   time.Time

	mu sync.Mutex // everything below is protected by mu

	Parent *SDDP_Inode

	dir *SDDP_DirInodeData

	Invalid     bool
	ImplicitDir bool

	fileHandles uint32

	userMetadata map[string][]byte
	s3Metadata   map[string][]byte

	// the refcnt is an exception, it's protected by the global lock
	// Goofys.mu
	refcnt uint64
}

func SDDP_NewInode(fs *SDDP, parent *SDDP_Inode, name *string, fullName *string) (inode *SDDP_Inode) {
	fmt.Println("sddp_handles.go/SDDP_NewInode called")
	inode = &SDDP_Inode{
		Name:       name,
		fs:         fs,
		AttrTime:   time.Now(),
		Parent:     parent,
		s3Metadata: make(map[string][]byte),
		refcnt:     1,
	}

	return
}

func (inode *SDDP_Inode) FullName() *string {
	if inode.Parent == nil {
		return inode.Name
	} else {
		s := inode.Parent.getChildName(*inode.Name)
		return &s
	}
}

func (inode *SDDP_Inode) touch() {
	inode.Attributes.Mtime = time.Now()
}

func (inode *SDDP_Inode) InflateAttributes() (attr fuseops.InodeAttributes) {
	mtime := inode.Attributes.Mtime
	if mtime.IsZero() {
		mtime = inode.fs.rootAttrs.Mtime
	}

	attr = fuseops.InodeAttributes{
		Size:   inode.Attributes.Size,
		Atime:  mtime,
		Mtime:  mtime,
		Ctime:  mtime,
		Crtime: mtime,
		Uid:    inode.fs.flags.Uid,
		Gid:    inode.fs.flags.Gid,
	}

	if inode.dir != nil {
		attr.Nlink = 2
		attr.Mode = inode.fs.flags.DirMode | os.ModeDir
	} else {
		attr.Nlink = 1
		attr.Mode = inode.fs.flags.FileMode
	}
	return
}

func (inode *SDDP_Inode) logFuse(op string, args ...interface{}) {
	if fuseLog.Level >= logrus.DebugLevel {
		fuseLog.Debugln(op, inode.Id, *inode.FullName(), args)
	}
}

func (inode *SDDP_Inode) errFuse(op string, args ...interface{}) {
	fuseLog.Errorln(op, inode.Id, *inode.FullName(), args)
}

func (inode *SDDP_Inode) ToDir() {
	inode.Attributes = SDDP_InodeAttributes{
		Size: 4096,
		// Mtime intentionally not initialized
	}
	inode.dir = &SDDP_DirInodeData{}
	inode.KnownSize = &inode.fs.rootAttrs.Size
}

func (parent *SDDP_Inode) findChild(name string) (inode *SDDP_Inode) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	inode = parent.findChildUnlockedFull(name)
	return
}

func (parent *SDDP_Inode) findInodeFunc(name string, isDir bool) func(i int) bool {
	// sort dirs first, then by name
	return func(i int) bool {
		if parent.dir.Children[i].isDir() != isDir {
			return isDir
		}
		return (*parent.dir.Children[i].Name) >= name
	}
}

func (parent *SDDP_Inode) findChildUnlockedFull(name string) (inode *SDDP_Inode) {
	inode = parent.findChildUnlocked(name, false)
	if inode == nil {
		inode = parent.findChildUnlocked(name, true)
	}
	return
}

func (parent *SDDP_Inode) findChildUnlocked(name string, isDir bool) (inode *SDDP_Inode) {
	l := len(parent.dir.Children)
	if l == 0 {
		return
	}
	i := sort.Search(l, parent.findInodeFunc(name, isDir))
	if i < l {
		// found
		if *parent.dir.Children[i].Name == name {
			inode = parent.dir.Children[i]
		}
	}
	return
}

func (parent *SDDP_Inode) findChildIdxUnlocked(name string) int {
	l := len(parent.dir.Children)
	if l == 0 {
		return -1
	}
	i := sort.Search(l, parent.findInodeFunc(name, true))
	if i < l {
		// found
		if *parent.dir.Children[i].Name == name {
			return i
		}
	}
	return -1
}

func (parent *SDDP_Inode) removeChildUnlocked(inode *SDDP_Inode) {
	l := len(parent.dir.Children)
	if l == 0 {
		return
	}
	i := sort.Search(l, parent.findInodeFunc(*inode.Name, inode.isDir()))
	if i >= l || *parent.dir.Children[i].Name != *inode.Name {
		panic(fmt.Sprintf("%v.removeName(%v) but child not found: %v",
			*parent.FullName(), *inode.Name, i))
	}

	copy(parent.dir.Children[i:], parent.dir.Children[i+1:])
	parent.dir.Children[l-1] = nil
	parent.dir.Children = parent.dir.Children[:l-1]

	if cap(parent.dir.Children)-len(parent.dir.Children) > 20 {
		tmp := make([]*SDDP_Inode, len(parent.dir.Children))
		copy(tmp, parent.dir.Children)
		parent.dir.Children = tmp
	}
}

func (parent *SDDP_Inode) removeChild(inode *SDDP_Inode) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	parent.removeChildUnlocked(inode)
	return
}

func (parent *SDDP_Inode) insertChild(inode *SDDP_Inode) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	parent.insertChildUnlocked(inode)
}

func (parent *SDDP_Inode) insertChildUnlocked(inode *SDDP_Inode) {
	l := len(parent.dir.Children)
	if l == 0 {
		parent.dir.Children = []*SDDP_Inode{inode}
		return
	}

	i := sort.Search(l, parent.findInodeFunc(*inode.Name, inode.isDir()))
	if i == l {
		// not found = new value is the biggest
		parent.dir.Children = append(parent.dir.Children, inode)
	} else {
		if *parent.dir.Children[i].Name == *inode.Name {
			panic(fmt.Sprintf("double insert of %v", parent.getChildName(*inode.Name)))
		}

		parent.dir.Children = append(parent.dir.Children, nil)
		copy(parent.dir.Children[i+1:], parent.dir.Children[i:])
		parent.dir.Children[i] = inode
	}
}

func (parent *SDDP_Inode) getChildName(name string) string {
	if parent.Id == fuseops.RootInodeID {
		return name
	} else {
		return fmt.Sprintf("%v/%v", *parent.FullName(), name)
	}
}

// LOCKS_REQUIRED(fs.mu)
// XXX why did I put lock required? This used to return a resurrect bool
// which no long does anything, need to look into that to see if
// that was legacy
func (inode *SDDP_Inode) Ref() {
	inode.logFuse("Ref", inode.refcnt)

	inode.refcnt++
	return
}

// LOCKS_REQUIRED(fs.mu)
func (inode *SDDP_Inode) DeRef(n uint64) (stale bool) {
	inode.logFuse("DeRef", n, inode.refcnt)

	if inode.refcnt < n {
		panic(fmt.Sprintf("deref %v from %v", n, inode.refcnt))
	}

	inode.refcnt -= n

	stale = (inode.refcnt == 0)
	return
}

func (parent *SDDP_Inode) Unlink(name string) (err error) {
	parent.logFuse("Unlink", name)

	fullName := parent.getChildName(name)

	params := &s3.DeleteObjectInput{
		Bucket: &parent.fs.bucket,
		Key:    parent.fs.key(fullName),
	}

	resp, err := parent.fs.s3.DeleteObject(params)
	if err != nil {
		return mapAwsError(err)
	}

	s3Log.Debug(resp)

	parent.mu.Lock()
	defer parent.mu.Unlock()

	inode := parent.findChildUnlocked(name, false)
	if inode != nil {
		parent.removeChildUnlocked(inode)
		inode.Parent = nil
	}

	return
}

func (parent *SDDP_Inode) Create(name string) (inode *SDDP_Inode, fh *SDDP_FileHandle) {
	fmt.Println("sddp_handles.go/Create called")
	parent.logFuse("Create", name)
	fullName := parent.getChildName(name)
	fs := parent.fs

	parent.mu.Lock()
	defer parent.mu.Unlock()

	now := time.Now()
	inode = SDDP_NewInode(fs, parent, &name, &fullName)
	inode.Attributes = SDDP_InodeAttributes{
		Size:  0,
		Mtime: now,
	}

	fh = SDDP_NewFileHandle(inode)
	fh.poolHandle = fs.bufferPool
	fh.buf = MBuf{}.Init(fh.poolHandle, 0, true)
	fh.dirty = true
	inode.fileHandles = 1

	parent.touch()

	return
}

func (parent *SDDP_Inode) MkDir(name string) (inode *SDDP_Inode, err error) {
	fmt.Println("sddp_handles.go/MkDir called")
	parent.logFuse("MkDir", name)

	fullName := parent.getChildName(name)
	fs := parent.fs

	params := &s3.PutObjectInput{
		Bucket: &fs.bucket,
		Key:    fs.key(fullName + "/"),
		Body:   nil,
	}

	if fs.flags.UseSSE {
		params.ServerSideEncryption = &fs.sseType
		if fs.flags.UseKMS && fs.flags.KMSKeyID != "" {
			params.SSEKMSKeyId = &fs.flags.KMSKeyID
		}
	}

	_, err = fs.s3.PutObject(params)
	if err != nil {
		err = mapAwsError(err)
		return
	}

	parent.mu.Lock()
	defer parent.mu.Unlock()

	inode = SDDP_NewInode(fs, parent, &name, &fullName)
	inode.ToDir()
	inode.touch()
	if parent.Attributes.Mtime.Before(inode.Attributes.Mtime) {
		parent.Attributes.Mtime = inode.Attributes.Mtime
	}

	return
}

func SDDP_isEmptyDir(fs *SDDP, fullName string) (isDir bool, err error) {
	fullName += "/"

	params := &s3.ListObjectsInput{
		Bucket:    &fs.bucket,
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int64(2),
		Prefix:    fs.key(fullName),
	}

	resp, err := fs.s3.ListObjects(params)
	if err != nil {
		return false, mapAwsError(err)
	}

	if len(resp.CommonPrefixes) > 0 || len(resp.Contents) > 1 {
		err = fuse.ENOTEMPTY
		isDir = true
		return
	}

	if len(resp.Contents) == 1 {
		isDir = true

		if *resp.Contents[0].Key != *fs.key(fullName) {
			err = fuse.ENOTEMPTY
		}
	}

	return
}

func (parent *SDDP_Inode) RmDir(name string) (err error) {
	parent.logFuse("Rmdir", name)

	fullName := parent.getChildName(name)
	fs := parent.fs

	isDir, err := SDDP_isEmptyDir(fs, fullName)
	if err != nil {
		return
	}
	if !isDir {
		return fuse.ENOENT
	}

	fullName += "/"

	params := &s3.DeleteObjectInput{
		Bucket: &fs.bucket,
		Key:    fs.key(fullName),
	}

	_, err = fs.s3.DeleteObject(params)
	if err != nil {
		return mapAwsError(err)
	}

	// we know this entry is gone
	parent.mu.Lock()
	defer parent.mu.Unlock()

	inode := parent.findChildUnlocked(name, true)
	if inode != nil {
		parent.removeChildUnlocked(inode)
		inode.Parent = nil
	}

	return
}

func (inode *SDDP_Inode) GetAttributes() (*fuseops.InodeAttributes, error) {
	// XXX refresh attributes
	inode.logFuse("GetAttributes")
	if inode.Invalid {
		return nil, fuse.ENOENT
	}
	attr := inode.InflateAttributes()
	return &attr, nil
}

func (inode *SDDP_Inode) isDir() bool {
	return inode.dir != nil
}

// LOCKS_REQUIRED(inode.mu)
func (inode *SDDP_Inode) fillXattrFromHead(resp *s3.HeadObjectOutput) {
	fmt.Println("sddp_handles.go/fillXattrFromHead called")
	inode.userMetadata = make(map[string][]byte)

	if resp.ETag != nil {
		inode.s3Metadata["etag"] = []byte(*resp.ETag)
	}
	if resp.StorageClass != nil {
		inode.s3Metadata["storage-class"] = []byte(*resp.StorageClass)
	} else {
		inode.s3Metadata["storage-class"] = []byte("STANDARD")
	}

	for k, v := range resp.Metadata {
		k = strings.ToLower(k)
		value, err := url.PathUnescape(*v)
		if err != nil {
			value = *v
		}
		inode.userMetadata[k] = []byte(value)
	}
}

// LOCKS_REQUIRED(inode.mu)
func (inode *SDDP_Inode) fillXattr() (err error) {
	fmt.Println("sddp_handles.go/fillXattr called")
	if !inode.ImplicitDir && inode.userMetadata == nil {

		fullName := *inode.FullName()
		if inode.isDir() {
			fullName += "/"
		}
		fs := inode.fs

		params := &s3.HeadObjectInput{
			Bucket: &fs.bucket,
			Key:    fs.key(fullName),
		}
		resp, err := fs.s3.HeadObject(params)
		if err != nil {
			err = mapAwsError(err)
			if err == fuse.ENOENT {
				err = nil
				if inode.isDir() {
					inode.ImplicitDir = true
				}
			}
			return err
		} else {
			inode.fillXattrFromHead(resp)
		}
	}

	return
}

// LOCKS_REQUIRED(inode.mu)
func (inode *SDDP_Inode) getXattrMap(name string, userOnly bool) (
	meta map[string][]byte, newName string, err error) {

	if strings.HasPrefix(name, "s3.") {
		if userOnly {
			return nil, "", syscall.EACCES
		}

		newName = name[3:]
		meta = inode.s3Metadata
	} else if strings.HasPrefix(name, "user.") {
		err = inode.fillXattr()
		if err != nil {
			return nil, "", err
		}

		newName = name[5:]
		meta = inode.userMetadata
	} else {
		if userOnly {
			return nil, "", syscall.EACCES
		} else {
			return nil, "", syscall.ENODATA
		}
	}

	if meta == nil {
		return nil, "", syscall.ENODATA
	}

	return
}

func SDDP_convertMetadata(meta map[string][]byte) (metadata map[string]*string) {
	metadata = make(map[string]*string)
	for k, v := range meta {
		metadata[k] = aws.String(xattrEscape(v))
	}
	return
}

// LOCKS_REQUIRED(inode.mu)
func (inode *SDDP_Inode) updateXattr() (err error) {
	err = SDDP_copyObjectMaybeMultipart(inode.fs, int64(inode.Attributes.Size),
		*inode.FullName(), *inode.FullName(),
		aws.String(string(inode.s3Metadata["etag"])), convertMetadata(inode.userMetadata))
	return
}

func (inode *SDDP_Inode) SetXattr(name string, value []byte, flags uint32) error {
	inode.logFuse("RemoveXattr", name)

	inode.mu.Lock()
	defer inode.mu.Unlock()

	meta, name, err := inode.getXattrMap(name, true)
	if err != nil {
		return err
	}

	if flags != 0x0 {
		_, ok := meta[name]
		if flags == 0x1 {
			if ok {
				return syscall.EEXIST
			}
		} else if flags == 0x2 {
			if !ok {
				return syscall.ENODATA
			}
		}
	}

	meta[name] = Dup(value)
	err = inode.updateXattr()
	return err
}

func (inode *SDDP_Inode) RemoveXattr(name string) error {
	inode.logFuse("RemoveXattr", name)

	inode.mu.Lock()
	defer inode.mu.Unlock()

	meta, name, err := inode.getXattrMap(name, true)
	if err != nil {
		return err
	}

	if _, ok := meta[name]; ok {
		delete(meta, name)
		err = inode.updateXattr()
		return err
	} else {
		return syscall.ENODATA
	}
}

func (inode *SDDP_Inode) GetXattr(name string) ([]byte, error) {
	inode.logFuse("GetXattr", name)

	inode.mu.Lock()
	defer inode.mu.Unlock()

	meta, name, err := inode.getXattrMap(name, false)
	if err != nil {
		return nil, err
	}

	value, ok := meta[name]
	if ok {
		return []byte(value), nil
	} else {
		return nil, syscall.ENODATA
	}
}

func (inode *SDDP_Inode) ListXattr() ([]string, error) {
	fmt.Println("sddp_handles.go/ListXattr called")
	inode.logFuse("ListXattr")

	inode.mu.Lock()
	defer inode.mu.Unlock()

	var xattrs []string

	err := inode.fillXattr()
	if err != nil {
		return nil, err
	}

	for k, _ := range inode.s3Metadata {
		xattrs = append(xattrs, "s3."+k)
	}

	for k, _ := range inode.userMetadata {
		xattrs = append(xattrs, "user."+k)
	}

	return xattrs, nil
}

func (inode *SDDP_Inode) OpenFile() (fh *SDDP_FileHandle, err error) {
	fmt.Println("sddp_handles.go/OpenFile called")
	inode.logFuse("OpenFile")

	inode.mu.Lock()
	defer inode.mu.Unlock()

	fh = SDDP_NewFileHandle(inode)
	inode.fileHandles += 1
	return
}

func (parent *SDDP_Inode) Rename(from string, newParent *SDDP_Inode, to string) (err error) {
	parent.logFuse("Rename", from, newParent.getChildName(to))

	fromFullName := parent.getChildName(from)
	fs := parent.fs

	var size int64
	var fromIsDir bool
	var toIsDir bool

	fromIsDir, err = SDDP_isEmptyDir(fs, fromFullName)
	if err != nil {
		// we don't support renaming a directory that's not empty
		return
	}

	toFullName := newParent.getChildName(to)

	toIsDir, err = SDDP_isEmptyDir(fs, toFullName)
	if err != nil {
		return
	}

	if fromIsDir && !toIsDir {
		_, err = fs.s3.HeadObject(&s3.HeadObjectInput{
			Bucket: &fs.bucket,
			Key:    fs.key(toFullName),
		})
		if err == nil {
			return fuse.ENOTDIR
		} else {
			err = mapAwsError(err)
			if err != fuse.ENOENT {
				return
			}
		}
	} else if !fromIsDir && toIsDir {
		return syscall.EISDIR
	}

	size = int64(-1)
	if fromIsDir {
		fromFullName += "/"
		toFullName += "/"
		size = 0
	}

	err = SDDP_renameObject(fs, size, fromFullName, toFullName)
	return
}

func SDDP_mpuCopyPart(fs *SDDP, from string, to string, mpuId string, bytes string, part int64,
	wg *sync.WaitGroup, srcEtag *string, etag **string, errout *error) {

	defer func() {
		wg.Done()
	}()

	// XXX use CopySourceIfUnmodifiedSince to ensure that
	// we are copying from the same object
	params := &s3.UploadPartCopyInput{
		Bucket:            &fs.bucket,
		Key:               fs.key(to),
		CopySource:        aws.String(pathEscape(from)),
		UploadId:          &mpuId,
		CopySourceRange:   &bytes,
		CopySourceIfMatch: srcEtag,
		PartNumber:        &part,
	}

	s3Log.Debug(params)

	resp, err := fs.s3.UploadPartCopy(params)
	if err != nil {
		s3Log.Errorf("UploadPartCopy %v = %v", params, err)
		*errout = mapAwsError(err)
		return
	}

	*etag = resp.CopyPartResult.ETag
	return
}

func SDDP_sizeToParts(size int64) int {
	const PART_SIZE = 5 * 1024 * 1024 * 1024

	nParts := int(size / PART_SIZE)
	if size%PART_SIZE != 0 {
		nParts++
	}
	return nParts
}

func SDDP_mpuCopyParts(fs *SDDP, size int64, from string, to string, mpuId string,
	wg *sync.WaitGroup, srcEtag *string, etags []*string, err *error) {

	const PART_SIZE = 5 * 1024 * 1024 * 1024

	rangeFrom := int64(0)
	rangeTo := int64(0)

	for i := int64(1); rangeTo < size; i++ {
		rangeFrom = rangeTo
		rangeTo = i * PART_SIZE
		if rangeTo > size {
			rangeTo = size
		}
		bytes := fmt.Sprintf("bytes=%v-%v", rangeFrom, rangeTo-1)

		wg.Add(1)
		go SDDP_mpuCopyPart(fs, from, to, mpuId, bytes, i, wg, srcEtag, &etags[i-1], err)
	}
}

func SDDP_copyObjectMultipart(fs *SDDP, size int64, from string, to string, mpuId string,
	srcEtag *string, metadata map[string]*string) (err error) {
	fmt.Println("sddp_handles.go/copyObjectMultipart called")
	var wg sync.WaitGroup
	nParts := sizeToParts(size)
	etags := make([]*string, nParts)

	if mpuId == "" {
		params := &s3.CreateMultipartUploadInput{
			Bucket:       &fs.bucket,
			Key:          fs.key(to),
			StorageClass: &fs.flags.StorageClass,
			ContentType:  fs.getMimeType(to),
			Metadata:     metadata,
		}

		if fs.flags.UseSSE {
			params.ServerSideEncryption = &fs.sseType
			if fs.flags.UseKMS && fs.flags.KMSKeyID != "" {
				params.SSEKMSKeyId = &fs.flags.KMSKeyID
			}
		}

		if fs.flags.ACL != "" {
			params.ACL = &fs.flags.ACL
		}

		resp, err := fs.s3.CreateMultipartUpload(params)
		if err != nil {
			return mapAwsError(err)
		}

		mpuId = *resp.UploadId
	}

	SDDP_mpuCopyParts(fs, size, from, to, mpuId, &wg, srcEtag, etags, &err)
	wg.Wait()

	if err != nil {
		return
	} else {
		parts := make([]*s3.CompletedPart, nParts)
		for i := 0; i < nParts; i++ {
			parts[i] = &s3.CompletedPart{
				ETag:       etags[i],
				PartNumber: aws.Int64(int64(i + 1)),
			}
		}

		params := &s3.CompleteMultipartUploadInput{
			Bucket:   &fs.bucket,
			Key:      fs.key(to),
			UploadId: &mpuId,
			MultipartUpload: &s3.CompletedMultipartUpload{
				Parts: parts,
			},
		}

		s3Log.Debug(params)

		_, err := fs.s3.CompleteMultipartUpload(params)
		if err != nil {
			s3Log.Errorf("Complete MPU %v = %v", params, err)
			return mapAwsError(err)
		}
	}

	return
}

func SDDP_copyObjectMaybeMultipart(fs *SDDP, size int64, from string, to string, srcEtag *string, metadata map[string]*string) (err error) {
	fmt.Println("sddp_handles.go/copyObjectMaybeMultipart called")
	if size == -1 || srcEtag == nil || metadata == nil {
		params := &s3.HeadObjectInput{
			Bucket: &fs.bucket,
			Key:    fs.key(from),
		}
		resp, err := fs.s3.HeadObject(params)
		if err != nil {
			return mapAwsError(err)
		}

		size = *resp.ContentLength
		metadata = resp.Metadata
		srcEtag = resp.ETag
	}

	from = fs.bucket + "/" + *fs.key(from)

	if size > 5*1024*1024*1024 {
		return SDDP_copyObjectMultipart(fs, size, from, to, "", srcEtag, metadata)
	}

	storageClass := fs.flags.StorageClass
	if size < 128*1024 && storageClass == "STANDARD_IA" {
		storageClass = "STANDARD"
	}

	params := &s3.CopyObjectInput{
		Bucket:            &fs.bucket,
		CopySource:        aws.String(pathEscape(from)),
		Key:               fs.key(to),
		StorageClass:      &storageClass,
		ContentType:       fs.getMimeType(to),
		Metadata:          metadata,
		MetadataDirective: aws.String(s3.MetadataDirectiveReplace),
	}

	s3Log.Debug(params)

	if fs.flags.UseSSE {
		params.ServerSideEncryption = &fs.sseType
		if fs.flags.UseKMS && fs.flags.KMSKeyID != "" {
			params.SSEKMSKeyId = &fs.flags.KMSKeyID
		}
	}

	if fs.flags.ACL != "" {
		params.ACL = &fs.flags.ACL
	}

	resp, err := fs.s3.CopyObject(params)
	if err != nil {
		s3Log.Errorf("CopyObject %v = %v", params, err)
		err = mapAwsError(err)
	}
	s3Log.Debug(resp)

	return
}

func SDDP_renameObject(fs *SDDP, size int64, fromFullName string, toFullName string) (err error) {
	err = SDDP_copyObjectMaybeMultipart(fs, size, fromFullName, toFullName, nil, nil)
	if err != nil {
		return err
	}

	delParams := &s3.DeleteObjectInput{
		Bucket: &fs.bucket,
		Key:    fs.key(fromFullName),
	}

	_, err = fs.s3.DeleteObject(delParams)
	if err != nil {
		return mapAwsError(err)
	}
	s3Log.Debugf("Deleted %v", delParams)

	return
}

func (parent *SDDP_Inode) addDotAndDotDot() {
	fmt.Println("sddp_handles.go/addDotAndDotDot called")
	fs := parent.fs
	en := &SDDP_DirHandleEntry{
		Name:       aws.String("."),
		Type:       fuseutil.DT_Directory,
		Attributes: &parent.Attributes,
		Offset:     1,
	}
	fs.insertInodeFromDirEntry(parent, en)
	dotDotAttr := &parent.Attributes
	if parent.Parent != nil {
		dotDotAttr = &parent.Parent.Attributes
	}
	en = &SDDP_DirHandleEntry{
		Name:       aws.String(".."),
		Type:       fuseutil.DT_Directory,
		Attributes: dotDotAttr,
		Offset:     2,
	}
	fs.insertInodeFromDirEntry(parent, en)
}

// if I had seen a/ and a/b, and now I get a/c, that means a/b is
// done, but not a/
func (parent *SDDP_Inode) isParentOf(inode *SDDP_Inode) bool {
	return inode.Parent != nil && (parent == inode.Parent || parent.isParentOf(inode.Parent))
}

func SDDP_sealPastDirs(dirs map[*SDDP_Inode]bool, d *SDDP_Inode) {
	for p, sealed := range dirs {
		if p != d && !sealed && !p.isParentOf(d) {
			dirs[p] = true
		}
	}
	// I just read something in d, obviously it's not done yet
	dirs[d] = false
}

func (parent *SDDP_Inode) insertSubTree(path string, obj *s3.Object, dirs map[*SDDP_Inode]bool) {
	fmt.Println("sddp_handles.go/insertSubTree called")
	fs := parent.fs
	slash := strings.Index(path, "/")
	if slash == -1 {
		fs.insertInodeFromDirEntry(parent, SDDP_objectToDirEntry(fs, obj, path, false))
		SDDP_sealPastDirs(dirs, parent)
	} else {
		dir := path[:slash]
		path = path[slash+1:]

		if len(path) == 0 {
			inode := fs.insertInodeFromDirEntry(parent, SDDP_objectToDirEntry(fs, obj, dir, true))
			inode.addDotAndDotDot()

			SDDP_sealPastDirs(dirs, inode)
		} else {
			// ensure that the potentially implicit dir is added
			en := &SDDP_DirHandleEntry{
				Name:       &dir,
				Type:       fuseutil.DT_Directory,
				Attributes: &fs.rootAttrs,
				Offset:     1,
			}
			inode := fs.insertInodeFromDirEntry(parent, en)
			// mark this dir but don't seal anything else
			// until we get to the leaf
			dirs[inode] = false

			inode.addDotAndDotDot()
			inode.insertSubTree(path, obj, dirs)
		}
	}
}

func (parent *SDDP_Inode) findChildMaxTime() time.Time {
	maxTime := parent.Attributes.Mtime

	for i, c := range parent.dir.Children {
		if i < 2 {
			// skip . and ..
			continue
		}
		if c.Attributes.Mtime.After(maxTime) {
			maxTime = c.Attributes.Mtime
		}
	}

	return maxTime
}

func (parent *SDDP_Inode) readDirFromCache(offset fuseops.DirOffset) (en *SDDP_DirHandleEntry, ok bool) {
	fmt.Println("sddp_handles.go/readDirFromCache called")
	parent.mu.Lock()
	defer parent.mu.Unlock()

	if !expired(parent.dir.DirTime, parent.fs.flags.TypeCacheTTL) {
		ok = true

		if int(offset) >= len(parent.dir.Children) {
			return
		}
		child := parent.dir.Children[offset]

		en = &SDDP_DirHandleEntry{
			Name:       child.Name,
			Inode:      child.Id,
			Offset:     offset + 1,
			Attributes: &child.Attributes,
		}
		if child.isDir() {
			en.Type = fuseutil.DT_Directory
		} else {
			en.Type = fuseutil.DT_File
		}

	}
	return
}

func (parent *SDDP_Inode) LookUpInodeNotDir(name string, c chan s3.HeadObjectOutput, errc chan error) {
	fmt.Println("sddp_handles.go/LookUpInodeNotDir called")
	params := &s3.HeadObjectInput{
		Bucket: &parent.fs.bucket,
		Key:    parent.fs.key(name),
	}
	resp, err := parent.fs.s3.HeadObject(params)
	if err != nil {
		errc <- mapAwsError(err)
		return
	}

	s3Log.Debug(resp)
	c <- *resp
}

func (parent *SDDP_Inode) LookUpInodeDir(name string, c chan s3.ListObjectsOutput, errc chan error) {
	fmt.Println("sddp_handles.go/LookUpInodeDir called")
	params := &s3.ListObjectsInput{
		Bucket:    &parent.fs.bucket,
		Delimiter: aws.String("/"),
		MaxKeys:   aws.Int64(1),
		Prefix:    parent.fs.key(name + "/"),
	}

	resp, err := parent.fs.s3.ListObjects(params)
	if err != nil {
		errc <- mapAwsError(err)
		return
	}

	s3Log.Debug(resp)
	c <- *resp
}
