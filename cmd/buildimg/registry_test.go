package main

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// redirect sends every request to target over plain HTTP, whatever host
// the request names, so a test can push to and pull from an in-process
// registry under a fixed, made-up registry host. (Image digests that
// depend on the registry prefix, like a base image naming its variants by
// digest, are then the same on every run.)
type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// useRegistry starts an in-process registry and points buildimg's pushes
// at it for the rest of the test. It returns the options to read images
// back with.
func useRegistry(t *testing.T) []remote.Option {
	t.Helper()
	srv := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	opts := []remote.Option{remote.WithTransport(redirect{target})}
	old := remoteOptions
	remoteOptions = opts
	t.Cleanup(func() { remoteOptions = old })
	return opts
}

// pulledIndex reads the image index at ref from the test registry.
func pulledIndex(t *testing.T, ref string, opts []remote.Option) v1.ImageIndex {
	t.Helper()
	r, err := name.ParseReference(ref)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := remote.Index(r, opts...)
	if err != nil {
		t.Fatalf("reading %s: %v", ref, err)
	}
	return idx
}
