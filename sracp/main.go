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

package main

import (
	"os"
	"strings"

	"github.com/mattrbianchi/twig"
	"github.com/mitre/fusera/sracp/cmd"
)

func init() {
	twig.SetFlags(twig.LstdFlags | twig.Lshortfile)
}

func main() {
	EnsurePathIsSet()
	cmd.Execute()
}

// mount -a seems to run goofys without PATH
// usually fusermount is in /bin
func EnsurePathIsSet() {
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "PATH=") {
			return
		}
	}

	os.Setenv("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
}
