![mitrelogo-black](static/mitrelogo-black.jpg)

Fusera
===

Fusera (FUSE for SRA) is a FUSE implementation for the cloud extension to the NCBI Sequence Read Archive (SRA).  SRA accepts data from all kinds of sequencing projects including clinically important studies that involve human subjects or their metagenomes, which may contain human sequences. These data often have a controlled access via dbGaP (the database of Genotypes and Phenotypes) .  The SRA provides access to cloud-hosted data through a web-services API (documented here) that provides signedURL access to data objects.   Fusera presents selected SRA data elements as a read-only file system, enabling users and tools to access the data through a file system interface.   The related sracp tool (reference) provides a convenient interface for copying the data to a mounted file system within a virtual machine.  These tools are intended for deployment on linux.

Fundamentally, Fusera presents all of the cloud-hosted SRA data for a set of SRA Accession numbers as a mounted directory, with one subdirectory per SRA Accession number.  The user’s credentials are passed through a dbGaP repository key, or ngc file,  that is obtained from dbGaP.  Fusera relies on the SRA’s Nameservice API (reference) which may limit the ability of fusera to ‘see’ certain data sets based on the location where the fusera service is deployed with the aim of limiting charges for data egress.

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

Access the help with `fusera -h`:

```
NAME:
   fusera - 

USAGE:
   fusera [global options] mountpoint
   
VERSION:
   0.0.-beta
   
GLOBAL OPTIONS:
   --ngc value       path to an ngc file that contains authentication info.
   --acc value       comma separated list of SRR#s that are to be mounted.
   --acc-file value  path to file with comma or space separated list of SRR#s that are to be mounted.
   --loc value       preferred region.
   
MISC OPTIONS:
   --help, -h       Print this help text and exit successfully.
   --debug          Enable debugging output.
   --debug_fuse     Enable fuse-related debugging output.
   --debug_service  Enable service-related debugging output.
   -f               Run fusera in foreground.
   --version, -v    print the version
```

A simple run of Fusera:
```ShellSession
$ fusera --ngc [path/to/ngcfile] --acc [comma separated list of SRR#s] --loc [s3.us-east-1|gs.US] <mountpoint>
```

The `<mountpoint>` must be an existing, empty directory, to which the user has read and write permissions.

It is recommended that the mountpoint be a directory owned by the user. Creating the mountpoint in system directories such as `/mnt`, `/tmp` have special uses in unix systems and should be avoided.

Because of the nature of FUSE systems, only the user who ran fusera will be able to read the files mounted. This can be changed by editing a config file (reference) on the machine to allow_others, but be warned that there are security implications to be considered: https://github.com/libfuse/libfuse#security-implications.

Accessions can be specified through the commmand line using the `--acc flag`, or, by reference to a file with space or comma separated accessions using the `--acc-file` option.   The union of these two sets of accessions is used to build the FUSE file system, with duplicates eliminated.

Troubleshooting
---

If you cannot successfully run fusera, first check that you have access to the SRA Nameservice API by invoking:
```
curl -X POST "https://www.ncbi.nlm.nih.gov/Traces/names_test/names.cgi?acc=1601726&version=xc-1.0&location=s3.us-east-1"
```
If this command has issues, contact your network administrator to resolve network/proxy issues.

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

