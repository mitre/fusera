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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"

	"github.com/jacobsa/fuse"
	"github.com/jacobsa/fuse/fuseops"
	"github.com/jacobsa/fuse/fuseutil"
)

type SDDP_DirInodeData struct {
	// these 2 refer to readdir of the Children
	lastOpenDir     *string
	lastOpenDirIdx  int
	seqOpenDirScore uint8
	DirTime         time.Time

	Children []*SDDP_Inode
}

type SDDP_DirHandleEntry struct {
	Name   *string
	Inode  fuseops.InodeID
	Type   fuseutil.DirentType
	Offset fuseops.DirOffset

	Attributes   *SDDP_InodeAttributes
	ETag         *string
	StorageClass *string
}

type SDDP_DirHandle struct {
	inode *SDDP_Inode

	mu         sync.Mutex // everything below is protected by mu
	Entries    []*SDDP_DirHandleEntry
	Marker     *string
	BaseOffset int
}

func SDDP_NewDirHandle(inode *SDDP_Inode) (dh *SDDP_DirHandle) {
	dh = &SDDP_DirHandle{inode: inode}
	return
}

func (inode *SDDP_Inode) OpenDir() (dh *SDDP_DirHandle) {
	fmt.Println("sddp_dir.go/OpenDir called")
	inode.logFuse("OpenDir")

	parent := inode.Parent
	if parent != nil && inode.fs.flags.TypeCacheTTL != 0 {
		parent.mu.Lock()
		defer parent.mu.Unlock()

		num := len(parent.dir.Children)

		if parent.dir.lastOpenDir == nil && num > 0 && *parent.dir.Children[0].Name == *inode.Name {
			if parent.dir.seqOpenDirScore < 255 {
				parent.dir.seqOpenDirScore += 1
			}
			// 2.1) if I open a/a, a/'s score is now 2
			// ie: handle the depth first search case
			if parent.dir.seqOpenDirScore >= 2 {
				fuseLog.Debugf("%v in readdir mode", *parent.FullName())
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
			parent.dir.lastOpenDirIdx += 1
			if parent.dir.seqOpenDirScore == 2 {
				fuseLog.Debugf("%v in readdir mode", *parent.FullName())
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
				fuseLog.Debugf("%v in readdir mode", *inode.FullName())
			}
		}
	}

	dh = SDDP_NewDirHandle(inode)
	return
}

// Dirents, sorted by name.
type SDDP_sortedDirents []*SDDP_DirHandleEntry

func (p SDDP_sortedDirents) Len() int           { return len(p) }
func (p SDDP_sortedDirents) Less(i, j int) bool { return *p[i].Name < *p[j].Name }
func (p SDDP_sortedDirents) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func (dh *SDDP_DirHandle) listObjectsSlurp(prefix string) (resp *s3.ListObjectsOutput, err error) {
	fmt.Println("dir.go/listObjectsSlurp called")
	var marker *string
	reqPrefix := prefix
	inode := dh.inode
	fs := inode.fs
	if dh.inode.Parent != nil {
		inode = dh.inode.Parent
		reqPrefix = *fs.key(*inode.FullName())
		if len(*inode.FullName()) != 0 {
			reqPrefix += "/"
		}
		marker = fs.key(*dh.inode.FullName() + "/")
	}

	params := &s3.ListObjectsInput{
		// Bucket: &fs.bucket,
		Prefix: &reqPrefix,
		Marker: marker,
	}

	resp, err = fs.s3.ListObjects(params)
	if err != nil {
		s3Log.Errorf("ListObjects %v = %v", params, err)
		return
	}

	num := len(resp.Contents)
	if num == 0 {
		return
	}

	dirs := make(map[*SDDP_Inode]bool)
	for _, obj := range resp.Contents {
		baseName := (*obj.Key)[len(reqPrefix):]

		slash := strings.Index(baseName, "/")
		if slash != -1 {
			inode.insertSubTree(baseName, obj, dirs)
		}
	}

	for d, sealed := range dirs {
		if d == dh.inode {
			// never seal the current dir because that's
			// handled at upper layer
			continue
		}

		if sealed || !*resp.IsTruncated {
			d.dir.DirTime = time.Now()
			d.Attributes.Mtime = d.findChildMaxTime()
		}
	}

	if *resp.IsTruncated {
		obj := resp.Contents[len(resp.Contents)-1]
		// if we are done listing prefix, we are good
		if strings.HasPrefix(*obj.Key, prefix) {
			// if we are done with all the slashes, then we are good
			baseName := (*obj.Key)[len(prefix):]

			for _, c := range baseName {
				if c <= '/' {
					// if an entry is ex: a!b, then the
					// next entry could be a/foo, so we
					// are not done yet.
					resp = nil
					break
				}
			}
		}
	}

	// we only return this response if we are totally done with listing this dir
	if resp != nil {
		resp.IsTruncated = aws.Bool(false)
		resp.NextMarker = nil
	}

	return
}

func (dh *SDDP_DirHandle) listObjects(prefix string) (resp *s3.ListObjectsOutput, err error) {
	fmt.Println("dir.go/listObjects called")
	errSlurpChan := make(chan error, 1)
	slurpChan := make(chan s3.ListObjectsOutput, 1)
	errListChan := make(chan error, 1)
	listChan := make(chan s3.ListObjectsOutput, 1)

	fs := dh.inode.fs

	// try to list without delimiter to see if we have slurp up
	// multiple directories
	if dh.Marker == nil &&
		fs.flags.TypeCacheTTL != 0 &&
		(dh.inode.Parent != nil && dh.inode.Parent.dir.seqOpenDirScore >= 2) {
		go func() {
			resp, err := dh.listObjectsSlurp(prefix)
			if err != nil {
				errSlurpChan <- err
			} else if resp != nil {
				slurpChan <- *resp
			} else {
				errSlurpChan <- fuse.EINVAL
			}
		}()
	} else {
		errSlurpChan <- fuse.EINVAL
	}

	listObjectsFlat := func() {
		params := &s3.ListObjectsInput{
			// Bucket:    &fs.bucket,
			Delimiter: aws.String("/"),
			Marker:    dh.Marker,
			Prefix:    &prefix,
		}

		resp, err := fs.s3.ListObjects(params)
		if err != nil {
			errListChan <- err
		} else {
			listChan <- *resp
		}
	}

	if !fs.flags.Cheap {
		// invoke the fallback in parallel if desired
		go listObjectsFlat()
	}

	// first see if we get anything from the slurp
	select {
	case resp := <-slurpChan:
		return &resp, nil
	case err = <-errSlurpChan:
	}

	if fs.flags.Cheap {
		listObjectsFlat()
	}

	// if we got an error (which may mean slurp is not applicable,
	// wait for regular list
	select {
	case resp := <-listChan:
		return &resp, nil
	case err = <-errListChan:
		return
	}
}

func SDDP_objectToDirEntry(fs *SDDP, obj *s3.Object, name string, isDir bool) (en *SDDP_DirHandleEntry) {
	if isDir {
		en = &SDDP_DirHandleEntry{
			Name:       &name,
			Type:       fuseutil.DT_Directory,
			Attributes: &fs.rootAttrs,
		}
	} else {
		en = &SDDP_DirHandleEntry{
			Name: &name,
			Type: fuseutil.DT_File,
			Attributes: &SDDP_InodeAttributes{
				Size:  uint64(*obj.Size),
				Mtime: *obj.LastModified,
			},
			ETag:         obj.ETag,
			StorageClass: obj.StorageClass,
		}
	}

	return
}

// LOCKS_REQUIRED(dh.mu)
func (dh *SDDP_DirHandle) ReadDir(offset fuseops.DirOffset) (en *SDDP_DirHandleEntry, err error) {
	fmt.Println("dir.go/ReadDir called")
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
		en = &SDDP_DirHandleEntry{
			Name:       aws.String("."),
			Type:       fuseutil.DT_Directory,
			Attributes: &fs.rootAttrs,
			Offset:     1,
		}
		return
	} else if offset == 1 {
		en = &SDDP_DirHandleEntry{
			Name:       aws.String(".."),
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

	if dh.Entries == nil {
		// try not to hold the lock when we make the request
		dh.mu.Unlock()

		prefix := *fs.key(*dh.inode.FullName())
		if len(*dh.inode.FullName()) != 0 {
			prefix += "/"
		}

		resp, err := dh.listObjects(prefix)
		if err != nil {
			dh.mu.Lock()
			return nil, mapAwsError(err)
		}

		s3Log.Debug(resp)
		dh.mu.Lock()

		dh.Entries = make([]*SDDP_DirHandleEntry, 0, len(resp.CommonPrefixes)+len(resp.Contents))

		// this is only returned for non-slurped responses
		for _, dir := range resp.CommonPrefixes {
			// strip trailing /
			dirName := (*dir.Prefix)[0 : len(*dir.Prefix)-1]
			// strip previous prefix
			dirName = dirName[len(*resp.Prefix):]
			if len(dirName) == 0 {
				continue
			}
			en = &SDDP_DirHandleEntry{
				Name:       &dirName,
				Type:       fuseutil.DT_Directory,
				Attributes: &fs.rootAttrs,
			}

			dh.Entries = append(dh.Entries, en)
		}

		lastDir := ""
		for _, obj := range resp.Contents {
			if !strings.HasPrefix(*obj.Key, prefix) {
				// other slurped objects that we cached
				continue
			}

			baseName := (*obj.Key)[len(prefix):]

			slash := strings.Index(baseName, "/")
			if slash == -1 {
				if len(baseName) == 0 {
					// shouldn't happen
					continue
				}
				dh.Entries = append(dh.Entries,
					SDDP_objectToDirEntry(fs, obj, baseName, false))
			} else {
				// this is a slurped up object which
				// was already cached, unless it's a
				// directory right under this dir that
				// we need to return
				dirName := baseName[:slash]
				if dirName != lastDir && lastDir != "" {
					// make a copy so we can take the address
					dir := lastDir
					en := &SDDP_DirHandleEntry{
						Name:       &dir,
						Type:       fuseutil.DT_Directory,
						Attributes: &fs.rootAttrs,
					}
					dh.Entries = append(dh.Entries, en)
				}
				lastDir = dirName
			}
		}
		if lastDir != "" {
			en := &SDDP_DirHandleEntry{
				Name:       &lastDir,
				Type:       fuseutil.DT_Directory,
				Attributes: &fs.rootAttrs,
			}
			dh.Entries = append(dh.Entries, en)
		}

		sort.Sort(SDDP_sortedDirents(dh.Entries))

		// Fix up offset fields.
		for i := 0; i < len(dh.Entries); i++ {
			en := dh.Entries[i]
			// offset is 1 based, also need to account for "." and ".."
			en.Offset = fuseops.DirOffset(i+dh.BaseOffset) + 1 + 2
		}

		if *resp.IsTruncated {
			dh.Marker = resp.NextMarker
		} else {
			dh.Marker = nil
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

func (dh *SDDP_DirHandle) CloseDir() error {
	return nil
}
