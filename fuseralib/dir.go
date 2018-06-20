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
	"sync"
	"time"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/awsutil"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

type DirInodeData struct {
	// these 2 refer to readdir of the Children
	lastOpenDir     *string
	lastOpenDirIdx  int
	seqOpenDirScore uint8
	DirTime         time.Time

	Children []*Inode
}

type DirHandleEntry struct {
	Name   *string
	Inode  fuseops.InodeID
	Type   fuseutil.DirentType
	Offset fuseops.DirOffset

	Attributes   *InodeAttributes
	ETag         *string
	StorageClass *string
}

type DirHandle struct {
	inode *Inode

	mu         sync.Mutex // everything below is protected by mu
	Entries    []*DirHandleEntry
	Marker     *string
	BaseOffset int
}

func NewDirHandle(inode *Inode) (dh *DirHandle) {
	dh = &DirHandle{inode: inode}
	return
}

func (inode *Inode) OpenDir() (dh *DirHandle) {
	parent := inode.Parent
	if parent != nil {
		parent.mu.Lock()
		defer parent.mu.Unlock()

		num := len(parent.dir.Children)

		if parent.dir.lastOpenDir == nil && num > 0 && *parent.dir.Children[0].Name == *inode.Name {
			if parent.dir.seqOpenDirScore < 255 {
				parent.dir.seqOpenDirScore++
			}
			// 2.1) if I open a/a, a/'s score is now 2
			// ie: handle the depth first search case
			if parent.dir.seqOpenDirScore >= 2 {
				// TODO: change to other log
				twig.Debugf("%v in readdir mode", *parent.FullName())
				// fuseLog.Debugf("%v in readdir mode", *parent.FullName())
			}
		} else if parent.dir.lastOpenDir != nil && parent.dir.lastOpenDirIdx+1 < num &&
			// we are reading the next one as expected
			*parent.dir.Children[parent.dir.lastOpenDirIdx+1].Name == *inode.Name &&
			// check that inode positions haven't moved
			*parent.dir.Children[parent.dir.lastOpenDirIdx].Name == *parent.dir.lastOpenDir {
			// 2.2) if I open b/, root's score is now 2
			// ie: handle the breath first search case
			if parent.dir.seqOpenDirScore < 255 {
				parent.dir.seqOpenDirScore++
			}
			parent.dir.lastOpenDirIdx++
			if parent.dir.seqOpenDirScore == 2 {
				//TODO: change to other log
				twig.Debugf("%v in readdir mode", *parent.FullName())
				//fuseLog.Debugf("%v in readdir mode", *parent.FullName())
			}
		} else {
			parent.dir.seqOpenDirScore = 0
			parent.dir.lastOpenDirIdx = parent.findChildIdxUnlocked(*inode.Name)
			if parent.dir.lastOpenDirIdx == -1 {
				panic(fmt.Sprintf("%v is not under %v", *inode.Name, *parent.FullName()))
			}
		}

		parent.dir.lastOpenDir = inode.Name
		inode.mu.Lock()
		defer inode.mu.Unlock()

		if inode.dir.lastOpenDir == nil {
			// 1) if I open a/, root's score = 1 (a is the first dir),
			// so make a/'s count at 1 too
			inode.dir.seqOpenDirScore = parent.dir.seqOpenDirScore
			if inode.dir.seqOpenDirScore >= 2 {
				twig.Debugf("%v in readdir mode", *inode.FullName())
				// log.FuseLog.Debugf("%v in readdir mode", *inode.FullName())
			}
		}
	}

	dh = NewDirHandle(inode)
	return
}

// Dirents, sorted by name.
type sortedDirents []*DirHandleEntry

func (p sortedDirents) Len() int           { return len(p) }
func (p sortedDirents) Less(i, j int) bool { return *p[i].Name < *p[j].Name }
func (p sortedDirents) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

// LOCKS_REQUIRED(dh.mu)
func (dh *DirHandle) ReadDir(offset fuseops.DirOffset) (en *DirHandleEntry, err error) {
	// If the request is for offset zero, we assume that either this is the first
	// call or rewinddir has been called. Reset state.
	if offset == 0 {
		dh.Entries = nil
	}

	en, ok := dh.inode.readDirFromCache(offset)
	if ok {
		return
	}

	fs := dh.inode.fs

	if offset == 0 {
		en = &DirHandleEntry{
			Name:       awsutil.String("."),
			Type:       fuseutil.DT_Directory,
			Attributes: &fs.rootAttrs,
			Offset:     1,
		}
		return
	} else if offset == 1 {
		en = &DirHandleEntry{
			Name:       awsutil.String(".."),
			Type:       fuseutil.DT_Directory,
			Attributes: &fs.rootAttrs,
			Offset:     2,
		}
		return
	}

	i := int(offset) - dh.BaseOffset - 2
	if i < 0 {
		panic(fmt.Sprintf("invalid offset %v, base=%v", offset, dh.BaseOffset))
	}

	if i >= len(dh.Entries) {
		if dh.Marker != nil {
			// we need to fetch the next page
			dh.Entries = nil
			dh.BaseOffset += i
			i = 0
		}
	}

	if i == len(dh.Entries) {
		// we've reached the end
		return nil, nil
	} else if i > len(dh.Entries) {
		return nil, fuse.EINVAL
	}

	return dh.Entries[i], nil
}

func (dh *DirHandle) CloseDir() error {
	return nil
}
