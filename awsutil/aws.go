// Copyright 2018 The MITRE Corporation
// Author Matthew Bianchi
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

package awsutil

import (
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws/credentials"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/jacobsa/fuse"
	"github.com/pkg/errors"
)

// HeadObject Makes an http HEAD request using the URL provided.
// URL should either point to a public obejct or be
// a signed URL giving the user GET permissions.
func HeadObject(url string) (*http.Response, error) {
	req, err := http.NewRequest("HEAD", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// GetObject Makes an http GET request using the URL provided.
// URL should either point to a public obejct or be
// a signed URL giving the user GET permissions.
// url: full url path to the object on AWS
func GetObject(url string) (*http.Response, error) {
	resp, err := GetObjectRange(url, "")
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// GetObjectRange Makes a ranged http GET request using the URL and byteRange provided.
// URL should either point to a public obejct or be
// a signed URL giving the user GET permissions.
// url: full url path to the object on AWS
// byteRange: the desired range of bytes of a file
// Should resemble the format for an http header Range.
// Example: "bytes="0-1000"
// Example: "bytes="1000-"
func GetObjectRange(url, byteRange string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if byteRange != "" {
		req.Header.Add("Range", byteRange)
	}
	// In case it's an FTP server, we want to prevent it from compressing the
	// file data.
	req.Header.Add("Accept-Encoding", "identity")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusPartialContent && resp.StatusCode != http.StatusOK {
		return nil, parseHTTPError(resp.StatusCode)
	}
	return resp, nil
}

// Client This strut provides a clean interface to making a requester pays type of
// request to the AWS API. Instead of having to construct the AWS configuration,
// client, session, and ObjectInput, one can simply provide the most basic fields
// and request an ObjectRange.
type Client struct {
	Bucket  string
	Key     string
	Region  string
	Profile string
}

// NewClient This function should be used to create a Client to avoid missing required fields.
func NewClient(bucket, key, region, profile string) Client {
	return Client{
		Bucket:  bucket,
		Key:     key,
		Region:  region,
		Profile: profile,
	}
}

// GetObjectRange Fetches the range of bytes from the file located at the destination on AWS
// derived from the Client's Bucket and Key fields.
func (c Client) GetObjectRange(byteRange string) (io.ReadCloser, error) {
	cfg := (&aws.Config{
		Credentials: credentials.NewSharedCredentials("", c.Profile),
		Region:      aws.String(c.Region),
	}).WithHTTPClient(newHTTPClient())
	sess := session.New(cfg)
	svc := s3.New(sess)
	input := &s3.GetObjectInput{
		Bucket:       aws.String(c.Bucket),
		Key:          aws.String(c.Key),
		Range:        aws.String(byteRange),
		RequestPayer: aws.String("requester"),
	}
	obj, err := svc.GetObject(input)
	return obj.Body, err
}

// ReadFile Expects the url to point to a valid ngc file.
// Uses the aws-sdk to read the file, assuming that
// this file will not be publicly accessible and will
// need to utilize aws credentials on the machine.
func ReadFile(path string) ([]byte, error) {
	// Users should be using virtual-hosted style:
	// http://[bucket].s3.amazonaws.com/[file]
	if !strings.Contains(path, "s3.amazonaws.com") {
		return nil, errors.Errorf("url did not point to a valid amazon s3 location or follow the virtual-hosted style of https://[bucket].[region].s3.amazonaws.com/[file]: %s", path)
	}
	u, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	sections := strings.Split(u.Hostname(), ".")
	if len(sections) < 5 {
		return nil, errors.Errorf("url did not point to a valid amazon s3 location or follow the virtual-hosted style of https://[bucket].[region].s3.amazonaws.com/[file]: %s", path)
	}
	bucket := sections[0]
	region := sections[1]
	file := u.Path
	cfg := (&aws.Config{
		Region: &region,
	}).WithHTTPClient(newHTTPClient())
	sess := session.New(cfg)
	svc := s3.New(sess)
	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(file),
	}
	obj, err := svc.GetObject(input)
	if err != nil {
		return nil, err
	}
	bytes, err := ioutil.ReadAll(obj.Body)
	return bytes, err
}

func newHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 15 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          1000,
			MaxIdleConnsPerHost:   1000,
			IdleConnTimeout:       20 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 10 * time.Second,
		},
	}
}

func parseHTTPError(code int) error {
	switch code {
	case 400:
		return fuse.EINVAL
	case 403:
		return syscall.EACCES
	case 404:
		return fuse.ENOENT
	case 405:
		return syscall.ENOTSUP
	case 500:
		return syscall.EAGAIN
	default:
		// TODO: re-evaluate whether this is a good move.
		return io.EOF
	}
}

func String(s string) *string {
	return &s
}

func Int64Value(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}
