Fusera has a lot to owe to the [Goofys](https://github.com/kahing/goofys) project: a high-performance, POSIX-ish file system written in Go.

Overview
---

Installation
---

## Handling Dependencies

### Linux

Install `fuse-utils`.

### Mac OS X

Install `osxfuse` via [Homebrew](http://brew.sh/):

```ShellSession
$ brew cask install osxfuse
```

## Installing

Build from source:

```ShellSession
$ export GOPATH=$HOME/go
$ go get github.com/mitre/fusera
$ go install github.com/mitre/fusera
```

Usage
---

```ShellSession
$ ./$GOPATH/bin/fusera --acc="" --loc="" <mountpoint>
```
Note that currently, the folder you want to mount to needs to already exist.

License
---

Copyright (C) 2015 - 2017 Ka-Hing Cheung

Licensed under the Apache License, Version 2.0

References
---

This project used the [Goofys](https://github.com/kahing/goofys) codebase as a starting point.

