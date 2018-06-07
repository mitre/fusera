// Modifications Copyright 2018 The MITRE Corporation
// Authors: Matthew Bianchi
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

package cmd

import (
	"fmt"
	"strings"

	"github.com/mattrbianchi/twig"
)

func prettyPrintError(err error) {
	// Accession errors
	if err.Error() == "no accessions provided" {
		twig.Debug(err)
		fmt.Println("No accessions provided: Fusera needs accession(s) in order to know what files to provide in its file system.")
	}
	if strings.Contains(err.Error(), "couldn't open cart file") {
		twig.Debug(err)
		fmt.Println("Bad cart file or path: Fusera interpreted the accession flag as a path to a cart file, but could not open the file at the path specified. Make sure the path leads to a valid cart file and that you have permissions to read that file. If you do and you're still getting this message, run with debug enabled for a more detailed error message and contact your IT administrator with its contents.")
	}
	if strings.Contains(err.Error(), "cart file was empty") {
		twig.Debug(err)
		fmt.Println("Bad cart file: Fusera interpreted the accession flag as a path to a cart file, but the file seems empty. Make sure the path leads to a valid cart file that has properly formatted contents and isn't corrupted. If you're still getting this message after assuring the file is correct, run with debug enabled for a more detailed error message and contact your IT administrator with its contents.")
	}

	// Location errors
	if err.Error() == "no location provided" {
		twig.Debug(err)
		fmt.Println("No location provided: A location was not provided so Fusera attempted to resolve the location itself and could not do so. This feature is only supported when Fusera is running on Amazon or Google's cloud platforms. If you are running on a server in either of these two cloud platforms and are still getting this message, run fusera with debug enabled for a more detailed error message and contact your IT administrator with its contents.")
	}

	// Ngc errors
	if strings.Contains(err.Error(), "couldn't open ngc file") {
		twig.Debug(err)
		fmt.Println("Bad ngc file path: Fusera tried to read the cart file at the path specified and couldn't. Make sure the path leads to a valid ngc file and that you have permissions to read that file. If you do and you're still getting this message, run with debug enabled for a more detailed error message and contact your IT administrator with its contents.")
	}

	// Filetype errors
	if err.Error() == "filetype was empty" {
		twig.Debug(err)
		fmt.Println("Filetype was empty: Fusera tried to parse the list of filetypes given but couldn't find anything. Example of a well formatted list to the filetype flag: -f \"bai,crai,cram\".")
	}

	// Mount errors
	if strings.Contains(err.Error(), "mountpoint doesn't exist") {
		twig.Debug(err)
		fmt.Println("Mountpoint doesn't exist: It seems like the directory you want to mount to does not exist. Please create the directory before trying again.")
	}
	if strings.Contains(err.Error(), "no such file or directory") {
		twig.Debug(err)
		fmt.Println("Failed to mount: It seems like the directory you want to mount to does not exist or you do not have correct permissions to access it. Please create the directory or correct the permissions on it before trying again.")
	}
	if strings.Contains(err.Error(), "EOF") {
		twig.Debug(err)
		fmt.Println("Failed to mount: It seems like the directory you want to mount to is already mounted by another instance of Fusera or another device. Choose another directory or try using the unmount command to unmount the other instance of Fusera before trying again. Be considerate of the unmount command, if anything is using Fusera while attempting to unmount, the unmount attempt will fail and that instance of Fusera will keep running.")
	}

	// API errors
	if strings.Contains(err.Error(), "failed to locate accessions") {
		twig.Debug(err)
		fmt.Println("Failed to locate accessions: It seems that Fusera has encountered an error while using the SRA Data Locator API to determine the file locations for accessions. This is an issue between Fusera and the API. In order to get more information, run Fusera with debug enabled and contact your IT administrator with its contents.")
	}

	// Fatal errors
	if strings.Contains(err.Error(), "FATAL") {
		twig.Debug(err)
		fmt.Println("Fatal: It seems like fusera encountered an internal issue, please run fusera with debug enabled for a more detailed error message and contact your IT administrator with its contents.")
	}
}
