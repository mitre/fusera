![mitrelogo-black](static/mitrelogo-black.jpg)

Fusera
===

Installation
---

### Linux

Depending on the distro, `fuse-utils` may need to be installed.

### Mac OS X

Mac users must install `osxfuse` either on their [website](https://osxfuse.github.io) or through [Homebrew](http://brew.sh/):

```ShellSession
$ brew cask install osxfuse
```

Releases of Fusera can be found at https://github.com/mitre/fusera/releases

For linux: `fusera-linux-amd64`

For mac: `fusera-darwin-amd64`

After downloading, it's advised to rename the file `fusera` to fit with the rest of this document.

Make sure to grab the latest release, which is signified on the left sidebar with a green badge. Also note that changing the binary to be executable will probably be necessary:
```ShellSession
chmod +x fusera
```

### Build from source:


```ShellSession
$ go get github.com/mitre/fusera/cmd/fusera
$ go install github.com/mitre/fusera/cmd/fusera
```

Usage
---

A simple run of Fusera:
```ShellSession
$ fusera --acc [comma separated list of SRR#s] --loc [s3.us-east-1|gs.US] <mountpoint>
```

It's important to note that currently, the `<mountpoint>` needs to already exist, be empty, and the user running fusera must have all permissions on that folder.

It is ill-advised to use a folder that is outside the user's directory, such as /mnt, /tmp, etc. These folders tend to be owned by `root` anyway but have special uses in unix systems and may cause problems.

Because of the nature of fuse systems, only the user who ran Fusera will be able to read the files mounted. This can be changed by editing a certain config file on the machine to `allow_others`, but be warned that there are security implications to be considered: https://github.com/libfuse/libfuse#security-implications.

When needing to use many accesions at once, one may be interested in the `--acc-file` flag. Fusera expects this file to be either comma or space separated. If the `--acc-file` and `--acc` flag are both used, a union of the two will be used without duplicates.

License
---

Fusera started its life as a hard fork of the [Goofys](https://github.com/kahing/goofys) project.

Copyright (C) 2015 - 2017 Ka-Hing Cheung

Modifications Copyright (C) 2018  The MITRE Corporation

> The modifications were developed for the NIH Cloud Commons Pilot. General questions can be forwarded to:
> 
> opensource@mitre.org  
> Technology Transfer Office  
> The MITRE Corporation  
> 7515 Colshire Drive  
> McLean, VA 22102-7539  

Licensed under the Apache License, Version 2.0

Only the functionality needed was retained from the Goofys project. Here are a list of files removed from the original source:
- api/api.go
- internal/
	- perms.go
	- ticket.go
	- ticket_test.go
	- v2signer.go
	- minio_test.go
	- goofys_test.go
	- aws_test.go

There has also been some refactoring of the codebase, so while some files have been removed, the code in them might exist in other files. License headers and copyright have been kept in these circumstances.

The major changes to the original source stem from Fusera's use case. Instead of only communicating with one bucket, Fusera is redesigned to be capable of accessing many different files distributed over multiple cloud services. One file can be in Google's Cloud Storage while another file that appears to be in the same folder can actually exist on an AWS S3 bucket. This flexibility is partly enabled by Fusera only needing read access to the files and therefore can use either public or signed urls to make HTTP requests directly to the cloud service's API.

So Goofys' use of the aws-sdk to interact with AWS compatible endpoints was removed for a more flexible way of communicating since Fusera has no need to authenticate for write access to any of these files.

Also, Goofys' start up was modified in order for Fusera to be able to communicate with an NIH API which provides the urls used to access the desired files.

References
---

Fusera has a lot to owe to the [Goofys](https://github.com/kahing/goofys) project: a high-performance, POSIX-ish file system written in Go. This was used as a starting point.

