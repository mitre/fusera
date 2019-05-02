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
	"encoding/json"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
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

// Makes an http HEAD request using the URL provided.
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

// Makes an http GET request using the URL provided.
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

// Makes a ranged http GET request using the URL and byteRange provided.
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

type Client struct {
	Bucket   string
	Key      string
	Region   string
	Profile  string
	Platform *Platform
}

func NewClient(bucket, key, region, profile string) Client {
	return Client{
		Bucket:  bucket,
		Key:     key,
		Region:  region,
		Profile: profile,
	}
}

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

// Expects the url to point to a valid ngc file.
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

// ResolveTraditionalLocation Forms the traditional location string.
func ResolveTraditionalLocation() (string, error) {
	platform, err := ResolveRegion()
	if err != nil {
		return "", err
	}
	return platform.Name + "." + platform.Region, nil
}

// ResolveRegion Attempt to resolve the location on aws or gs.
func ResolveRegion() (*Platform, error) {
	platform := &Platform{}
	region, err := resolveAwsRegion()
	if err != nil {
		// could be on google
		// retain aws error message
		msg := err.Error()
		region, err = resolveGcpZone()
		if err != nil {
			// return both aws and google error messages
			return nil, errors.Wrap(err, msg)
		}
		platform.Name = "gs"
		platform.Region = region
		return platform, nil
	}
	platform.Name = "s3"
	platform.Region = region
	return platform, nil
}

// Platform contains data that describes the cloud platform Fusera is running on.
type Platform struct {
	Name          string
	Region        string
	InstanceToken []byte
}

// IsAWS returns true if the platform is AWS.
func (p *Platform) IsAWS() bool {
	return p.Name == "s3"
}

// IsGCP returns true if the platform is GCP.
func (p *Platform) IsGCP() bool {
	return p.Name == "gs"
}

// NewManualPlatform creates a plaftorm from a manually set location
func NewManualPlatform(location string) (*Platform, error) {
	ss := strings.Split(location, ".")
	if len(ss) != 2 {
		return nil, errors.New("location given contained more than one \".\" which means it cannot be parsed according to expected cloud.region")
	}
	var cloud, region string = ss[0], ss[1]
	return &Platform{Name: cloud, Region: region}, nil
}

// NewAwsPlatform creates a plaftorm with s3 as the Name and takes an AWS
// region.
func NewAwsPlatform(region string) *Platform {
	return &Platform{Name: "s3", Region: region}
}

// NewGcpPlatform creates a platform with gs as the Name and takes a GCP zone.
func NewGcpPlatform(region string) *Platform {
	return &Platform{Name: "gs", Region: region}
}

// FindLocation attempts to figure out which cloud
// provider Fusera is running on and what region of that cloud.
func FindLocation() (*Platform, error) {
	p := &Platform{}
	aws, err := resolveAwsRegion()
	if err != nil {
		// could be on google
		// retain aws error message
		msg := err.Error()
		token, err := retrieveGCPInstanceToken()
		if err != nil {
			// return both aws and google error messages
			return nil, errors.Wrap(err, msg)
		}
		zone, err := resolveGcpZone()
		if err != nil {
			// return both aws and google error messages
			return nil, errors.Wrap(err, msg)
		}
		p.Name = "gs"
		p.Region = zone
		p.InstanceToken = token
		return p, nil
	}
	p.Name = "s3"
	p.Region = aws
	return p, nil
}

func retrieveGCPInstanceToken() ([]byte, error) {
	// make a request to token endpoint
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   1 * time.Second,
				KeepAlive: 1 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          1000,
			MaxIdleConnsPerHost:   1000,
			IdleConnTimeout:       500 * time.Millisecond,
			TLSHandshakeTimeout:   500 * time.Millisecond,
			ExpectContinueTimeout: 500 * time.Millisecond,
		},
	}
	req, err := http.NewRequest("GET", "http://metadata/computeMetadata/v1/instance/service-accounts/default/identity?audience=https://www.ncbi.nlm.nih.gov&format=full", nil)
	req.Header.Add("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.Wrapf(err, "location was not provided, fusera attempted to resolve region but encountered an error, this feature only works when fusera is on an amazon or google instance")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.Errorf("issue trying to retreive GCP instance token, got: %d: %s", resp.StatusCode, resp.Status)
	}
	token, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.New("issue trying to resolve region, couldn't decode response from google")
	}
	return token, nil
}

func resolveAwsRegion() (string, error) {
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   1 * time.Second,
				KeepAlive: 1 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          1000,
			MaxIdleConnsPerHost:   1000,
			IdleConnTimeout:       500 * time.Millisecond,
			TLSHandshakeTimeout:   500 * time.Millisecond,
			ExpectContinueTimeout: 500 * time.Millisecond,
		},
	}
	// maybe we are on an AWS instance and can resolve what region we are in.
	// let's try it out and if we timeout we'll return an error.
	// use this url: http://169.254.169.254/latest/dynamic/instance-identity/document
	resp, err := client.Get("http://169.254.169.254/latest/dynamic/instance-identity/document")
	if err != nil {
		return "", errors.Wrapf(err, "location was not provided, fusera attempted to resolve region but encountered an error, this feature only works when fusera is on an amazon or google instance")
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf("issue trying to resolve region, got: %d: %s", resp.StatusCode, resp.Status)
	}
	var payload struct {
		Region string `json:"region"`
	}
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return "", errors.New("issue trying to resolve region, couldn't decode response from amazon")
	}
	if payload.Region == "" {
		return "", errors.New("issue trying to resolve region, amazon returned empty region")
	}
	return payload.Region, nil
}

func resolveGcpZone() (string, error) {
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   1 * time.Second,
				KeepAlive: 1 * time.Second,
				DualStack: true,
			}).DialContext,
			MaxIdleConns:          1000,
			MaxIdleConnsPerHost:   1000,
			IdleConnTimeout:       500 * time.Millisecond,
			TLSHandshakeTimeout:   500 * time.Millisecond,
			ExpectContinueTimeout: 500 * time.Millisecond,
		},
	}
	req, err := http.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/zone?alt=json", nil)
	req.Header.Add("Metadata-Flavor", "Google")
	resp, err := client.Do(req)
	if err != nil {
		return "", errors.Wrapf(err, "location was not provided, fusera attempted to resolve region but encountered an error, this feature only works when fusera is on an amazon or google instance")
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.Errorf("issue trying to resolve region, got: %d: %s", resp.StatusCode, resp.Status)
	}
	var payload string
	err = json.NewDecoder(resp.Body).Decode(&payload)
	if err != nil {
		return "", errors.New("issue trying to resolve region, couldn't decode response from google")
	}
	path := filepath.Base(payload)
	if path == "" || len(path) == 1 {
		return "", errors.New("issue trying to resolve region, google returned empty region")
	}
	return path, nil
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
