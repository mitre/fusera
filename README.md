![mitrelogo-black](static/mitrelogo-black.jpg)

Fusera
===

Fusera (FUSE for SRA) is a [FUSE](https://en.wikipedia.org/wiki/Filesystem_in_Userspace) implementation for the cloud extension to the [NCBI Sequence Read Archive (SRA)](https://www.ncbi.nlm.nih.gov/sra). SRA accepts data from all kinds of sequencing projects including clinically important studies that involve human subjects or their metagenomes, which may contain human sequences. These data often have a controlled access via [dbGaP (the database of Genotypes and Phenotypes)](https://www.ncbi.nlm.nih.gov/gap/). The SRA provides access to cloud-hosted data through a web-services API (documented here) that provides signedURL access to data objects. Fusera presents selected SRA data elements as a read-only file system, enabling users and tools to access the data through a file system interface. The related sracp tool (reference) provides a convenient interface for copying the data to a mounted file system within a virtual machine. These tools are intended for deployment on linux.

Fundamentally, Fusera presents all of the cloud-hosted SRA data for a set of SRA Accession numbers as a mounted directory, with one subdirectory per SRA Accession number. The user’s credentials are passed through a [dbGaP repository key](https://www.ncbi.nlm.nih.gov/books/NBK63512/), or ngc file, that is obtained from dbGaP. Fusera relies on the SRA’s Nameservice API (reference) which may limit the ability of fusera to ‘see’ certain data sets based on the location where the fusera service is deployed with the aim of limiting charges for data egress.

Installation
---

Note that it is in the works to have both `apt-get` packages and `homebrew` packages to ease the install process.

### Dependencies

#### Fusera

Depending on the linux distro, `fuse-utils` may need to be installed.

Mac users must install `osxfuse` either on their [website](https://osxfuse.github.io) or through [Homebrew](http://brew.sh/):

```ShellSession
$ brew cask install osxfuse
```

#### Sracp

For now, `sracp` requires an installation of `curl`. This is an "alpha" solution and it's in the roadmap to remove this dependency.

### Pre-built Releases

For easy installation, releases of Fusera and Sracp can be found at https://github.com/mitre/fusera/releases

For linux: `fusera-linux-amd64` and `sracp-linux-amd64`

For mac: `fusera-darwin-amd64` and `sracp-darwin-amd64`

After downloading, it's advised to rename the files to `fusera` and `sracp` to fit with the rest of this document.

Make sure to grab the latest release, which is signified on the left sidebar with a green badge. Also note that changing the binary to be executable will probably be necessary:
```ShellSession
chmod +x fusera
chmod +x sracp
```

It is advised to move this file somewhere in your PATH in order to increase ease of calling either `fusera` or `sracp`.

### Build from source:


```ShellSession
$ go get github.com/mitre/fusera/cmd/fusera
$ go install github.com/mitre/fusera/cmd/fusera
$ go get github.com/mitre/fusera/cmd/sracp
$ go install github.com/mitre/fusera/cmd/sracp
```

Usage
---

In an effort to keep the instructions as up to date as possible, please refer to the wiki for instructions on how to use `fusera` or `sracp`:  
https://github.com/mitre/fusera/wiki/Running-Fusera  
https://github.com/mitre/fusera/wiki/Running-Sracp  

Troubleshooting
---

https://github.com/mitre/fusera/wiki/Troubleshooting

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

