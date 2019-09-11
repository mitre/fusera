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
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/awsutil"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

type InodeAttributes struct {
	Size           uint64
	Mtime          time.Time
	ExpirationDate time.Time
}

type Inode struct {
	ID          fuseops.InodeID
	Name        *string
	Link        string
	Acc         string
	ErrContents string
	fs          *Fusera
	Attributes  InodeAttributes
	KnownSize   *uint64
	AttrTime    time.Time
	ReqPays     bool
	Bucket      string
	Key         string
	Region      string
	CeRequired  bool

	mu sync.Mutex // everything below is protected by mu

	Parent *Inode

	dir *DirInodeData

	Invalid     bool
	ImplicitDir bool

	fileHandles uint32

	userMetadata map[string][]byte
	s3Metadata   map[string][]byte

	// the refcnt is an exception, it's protected by the global lock
	// Goofys.mu
	refcnt uint64
}

func NewInode(fs *Fusera, parent *Inode, name *string, fullName *string) (inode *Inode) {
	inode = &Inode{
		Name:       name,
		fs:         fs,
		AttrTime:   time.Now(),
		Parent:     parent,
		s3Metadata: make(map[string][]byte),
		refcnt:     1,
	}

	return
}

func (inode *Inode) FullName() *string {
	if inode.Parent == nil {
		return inode.Name
	}
	s := inode.Parent.getChildName(*inode.Name)
	return &s
}

func (inode *Inode) touch() {
	inode.Attributes.Mtime = time.Now()
}

func (inode *Inode) InflateAttributes() (attr fuseops.InodeAttributes) {
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
		Uid:    inode.fs.opt.UID,
		Gid:    inode.fs.opt.GID,
	}

	if inode.dir != nil {
		attr.Nlink = 2
		attr.Mode = inode.fs.DirMode | os.ModeDir
	} else {
		attr.Nlink = 1
		attr.Mode = inode.fs.FileMode
	}
	return
}

// func (inode *Inode) logFuse(op string, args ...interface{}) {
// 	if log.FuseLog.Level >= logrus.DebugLevel {
// 		log.FuseLog.Debugln(op, inode.Id, *inode.FullName(), args)
// 	}
// }

// func (inode *Inode) errFuse(op string, args ...interface{}) {
// 	log.FuseLog.Errorln(op, inode.Id, *inode.FullName(), args)
// }

func (inode *Inode) ToDir() {
	inode.Attributes = InodeAttributes{
		Size: 4096,
		// Mtime intentionally not initialized
	}
	inode.dir = &DirInodeData{}
	inode.KnownSize = &inode.fs.rootAttrs.Size
}

func (parent *Inode) findChild(name string) (inode *Inode) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	inode = parent.findChildUnlockedFull(name)
	return
}

func (parent *Inode) findInodeFunc(name string, isDir bool) func(i int) bool {
	// sort dirs first, then by name
	return func(i int) bool {
		if parent.dir.Children[i].isDir() != isDir {
			return isDir
		}
		return (*parent.dir.Children[i].Name) >= name
	}
}

func (parent *Inode) findChildUnlockedFull(name string) (inode *Inode) {
	inode = parent.findChildUnlocked(name, false)
	if inode == nil {
		inode = parent.findChildUnlocked(name, true)
	}
	return
}

func (parent *Inode) findChildUnlocked(name string, isDir bool) (inode *Inode) {
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

func (parent *Inode) findChildIdxUnlocked(name string) int {
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

func (parent *Inode) removeChildUnlocked(inode *Inode) {
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
		tmp := make([]*Inode, len(parent.dir.Children))
		copy(tmp, parent.dir.Children)
		parent.dir.Children = tmp
	}
}

func (parent *Inode) removeChild(inode *Inode) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	parent.removeChildUnlocked(inode)
	return
}

func (parent *Inode) insertChild(inode *Inode) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	parent.insertChildUnlocked(inode)
}

func (parent *Inode) insertChildUnlocked(inode *Inode) {
	l := len(parent.dir.Children)
	if l == 0 {
		parent.dir.Children = []*Inode{inode}
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

func (parent *Inode) getChildName(name string) string {
	if parent.ID == fuseops.RootInodeID {
		return name
	}
	return fmt.Sprintf("%v/%v", *parent.FullName(), name)
}

// LOCKS_REQUIRED(fs.mu)
// XXX why did I put lock required? This used to return a resurrect bool
// which no long does anything, need to look into that to see if
// that was legacy
func (inode *Inode) Ref() {
	// inode.logFuse("Ref", inode.refcnt)

	inode.refcnt++
	return
}

// LOCKS_REQUIRED(fs.mu)
func (inode *Inode) DeRef(n uint64) (stale bool) {
	// inode.logFuse("DeRef", n, inode.refcnt)

	if inode.refcnt < n {
		panic(fmt.Sprintf("deref %v from %v", n, inode.refcnt))
	}

	inode.refcnt -= n

	stale = (inode.refcnt == 0)
	return
}

func (inode *Inode) GetAttributes() (*fuseops.InodeAttributes, error) {
	// XXX refresh attributes
	// inode.logFuse("GetAttributes")
	if inode.Invalid {
		return nil, fuse.ENOENT
	}
	attr := inode.InflateAttributes()
	return &attr, nil
}

func (inode *Inode) isDir() bool {
	return inode.dir != nil
}

// LOCKS_REQUIRED(inode.mu)
func (inode *Inode) fillXattr() (err error) {
	return
}

// LOCKS_REQUIRED(inode.mu)
func (inode *Inode) getXattrMap(name string, userOnly bool) (meta map[string][]byte, newName string, err error) {

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
		}
		return nil, "", syscall.ENODATA
	}

	if meta == nil {
		return nil, "", syscall.ENODATA
	}

	return
}

func (inode *Inode) GetXattr(name string) ([]byte, error) {
	// inode.logFuse("GetXattr", name)

	inode.mu.Lock()
	defer inode.mu.Unlock()

	meta, name, err := inode.getXattrMap(name, false)
	if err != nil {
		return nil, err
	}

	value, ok := meta[name]
	if ok {
		return []byte(value), nil
	}
	return nil, syscall.ENODATA
}

func (inode *Inode) ListXattr() ([]string, error) {
	// inode.logFuse("ListXattr")

	inode.mu.Lock()
	defer inode.mu.Unlock()

	var xattrs []string

	err := inode.fillXattr()
	if err != nil {
		return nil, err
	}

	for k := range inode.s3Metadata {
		xattrs = append(xattrs, "s3."+k)
	}

	for k := range inode.userMetadata {
		xattrs = append(xattrs, "user."+k)
	}

	return xattrs, nil
}

func (inode *Inode) OpenFile() (fh *FileHandle, err error) {
	// inode.logFuse("OpenFile")

	inode.mu.Lock()
	defer inode.mu.Unlock()

	fh = NewFileHandle(inode)
	inode.fileHandles++
	return
}

func (parent *Inode) addDotAndDotDot() {
	twig.Debug("handle.go/addDotAndDotDot called")
	fs := parent.fs
	en := &DirHandleEntry{
		Name:       awsutil.String("."),
		Type:       fuseutil.DT_Directory,
		Attributes: &parent.Attributes,
		Offset:     1,
	}
	fs.insertInodeFromDirEntry(parent, en)
	dotDotAttr := &parent.Attributes
	if parent.Parent != nil {
		dotDotAttr = &parent.Parent.Attributes
	}
	en = &DirHandleEntry{
		Name:       awsutil.String(".."),
		Type:       fuseutil.DT_Directory,
		Attributes: dotDotAttr,
		Offset:     2,
	}
	fs.insertInodeFromDirEntry(parent, en)
}

// if I had seen a/ and a/b, and now I get a/c, that means a/b is
// done, but not a/
func (parent *Inode) isParentOf(inode *Inode) bool {
	return inode.Parent != nil && (parent == inode.Parent || parent.isParentOf(inode.Parent))
}

func (parent *Inode) findChildMaxTime() time.Time {
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

func (parent *Inode) readDirFromCache(offset fuseops.DirOffset) (en *DirHandleEntry, ok bool) {
	parent.mu.Lock()
	defer parent.mu.Unlock()

	ok = true

	if int(offset) >= len(parent.dir.Children) {
		return
	}
	child := parent.dir.Children[offset]

	en = &DirHandleEntry{
		Name:       child.Name,
		Inode:      child.ID,
		Offset:     offset + 1,
		Attributes: &child.Attributes,
	}
	if child.isDir() {
		en.Type = fuseutil.DT_Directory
	} else {
		en.Type = fuseutil.DT_File
	}

	return
}
