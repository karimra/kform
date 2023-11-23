package pkgio

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/henderiw-nephio/kform/tools/apis/kform/pkg/meta/v1alpha1"
	"github.com/henderiw-nephio/kform/tools/pkg/fsys"
	"github.com/henderiw-nephio/kform/tools/pkg/pkgio/grabber"
	"github.com/henderiw-nephio/kform/tools/pkg/pkgio/ignore"
	"github.com/henderiw-nephio/kform/tools/pkg/pkgio/oci"
	"github.com/henderiw-nephio/kform/tools/pkg/pkgio/oras"
	"github.com/henderiw-nephio/kform/tools/pkg/syntax/address"
	"github.com/henderiw/logger/log"
	"gopkg.in/yaml.v2"
)

type PkgPushReadWriter interface {
	Reader
	Writer
}

func NewPkgPushReadWriter(srcPath string, pkg *address.Package, releaser bool) PkgPushReadWriter {
	// TBD do we add validation here
	// Ignore file processing should be done here
	fs := fsys.NewDiskFS(srcPath)
	ignoreRules := ignore.Empty(IgnoreFileMatch[0])
	return &pkgPushReadWriter{
		reader: &PkgReader{
			PathExists:     true,
			Fsys:           fsys.NewDiskFS(srcPath),
			MatchFilesGlob: MatchAll,
			IgnoreRules:    ignoreRules,
		},
		writer: &pkgPushWriter{
			fsys:     fs,
			rootPath: srcPath,
			pkg:      pkg,
			releaser: releaser,
		},
	}
}

type pkgPushReadWriter struct {
	reader *PkgReader
	writer *pkgPushWriter
}

func (r *pkgPushReadWriter) Read(ctx context.Context, data *Data) (*Data, error) {
	return r.reader.Read(ctx, data)
}

func (r *pkgPushReadWriter) Write(ctx context.Context, data *Data) error {
	return r.writer.write(ctx, data)
}

type pkgPushWriter struct {
	fsys     fsys.FS
	rootPath string
	pkg      *address.Package
	releaser bool
}

func (r *pkgPushWriter) write(ctx context.Context, data *Data) error {
	log := log.FromContext(ctx)
	// get the kform file to determine is this a provider or a module
	// if there is no kformfile or we cannot find the provider/module
	// information we fail
	d, err := data.Get(PkgFileMatch[0])
	if err != nil {
		return err
	}
	kformFile := v1alpha1.KformFile{}
	if err := yaml.Unmarshal([]byte(d), &kformFile); err != nil {
		return err
	}
	if err := kformFile.Spec.Kind.Validate(); err != nil {
		return err
	}

	if kformFile.Spec.Kind == v1alpha1.PkgKindProvider {
		if r.releaser {
			// get image from the release github page
			releases, err := r.pkg.GetReleases(ctx)
			if err != nil {
				return fmt.Errorf("cannot get releases for pkg: %s, err: %s", r.pkg.GetRef(), err.Error())
			}
			// find the release, matching the version supplied
			release := releases.GetRelease(r.pkg.SelectedVersion)
			if release == nil {
				return fmt.Errorf("cannot find release for pkg: %s", r.pkg.GetRef())
			}
			images := release.GetImageData(ctx)
			// download images
			// TODO optimize in memory store -> we store in the local dir for now
			fileLocs := map[string][]string{}
			for _, image := range images {
				fileLocs[image.Name] = []string{image.URL}
			}
			if err := r.downloadImages(ctx, fileLocs); err != nil {
				return fmt.Errorf("cannot find release for pkg: %s", r.pkg.GetRef())
			}
			for _, image := range images {
				var pkg *address.Package
				*pkg = *r.pkg
				pkg.Platform = &address.Platform{
					OS:   runtime.GOOS,
					Arch: runtime.GOARCH,
				}

				fsys := fsys.NewDiskFS(".")
				img, err := fsys.ReadFile(image.Name)
				if err != nil {
					log.Error("cannot read file, just downloaded", "fileName", image.Name, "error", err.Error())
					continue
				}
				return r.pushPackage(ctx, kformFile.Spec.Kind, pkg.GetRef(), data, img)
			}
		} else {
			// the os and arch are determined locally for local pushed provider packages
			// the image data need to be split from the other package data
			var img []byte
			images := 0
			for path, b := range data.List() {
				// if the data is an image we delete the
				if strings.HasPrefix(path, "image") {
					if images > 0 {
						log.Error("a provider pkg can only have 1 image")
						return fmt.Errorf("a locally pushed package can only have 1 image")
					}
					img = []byte(b)
					data.Delete(path)
					images++
				}
			}
			r.pkg.Platform = &address.Platform{
				OS:   runtime.GOOS,
				Arch: runtime.GOARCH,
			}
			return r.pushPackage(ctx, kformFile.Spec.Kind, r.pkg.GetRef(), data, img)
		}
	}
	// this is a module
	// the runtime OS and ARCH does not matter for a module -> we supply the simple ref
	return r.pushPackage(ctx, kformFile.Spec.Kind, r.pkg.GetRef(), data, nil)
}

func (r *pkgPushWriter) pushPackage(ctx context.Context, pkgKind v1alpha1.PkgKind, ref string, pkgData *Data, imgByte []byte) error {
	log := log.FromContext(ctx).With("pkgKind", pkgKind, "pkgName", ref)
	// build a zipped tar bal from the pkgData in the pkg
	pkgByte, err := oci.BuildTgz(pkgData.List())
	if err != nil {
		log.Error("failed to build zipped tarbal from pkg", "error", err)
		return err
	}
	// the image is already zipped
	if err := oras.Push(ctx, pkgKind, ref, pkgByte, imgByte); err != nil {
		log.Error("failed to push pkg", "error", err)
		return err
	}
	log.Info("pkg push succeeded")
	return nil
}

func (r *pkgPushWriter) downloadImages(ctx context.Context, fileLocs map[string][]string) error {
	respch, err := grabber.GetBatch(ctx, 3, fileLocs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return err
	}

	// start a ticker to update progress every 200ms
	t := time.NewTicker(200 * time.Millisecond)

	// monitor downloads
	completed := 0
	inProgress := 0
	responses := make([]*grabber.Response, 0)
	for completed < grabber.GetTotalURLs(fileLocs) {
		select {
		case resp := <-respch:
			// a new response has been received and has started downloading
			// (nil is received once, when the channel is closed by grab)
			if resp != nil {
				responses = append(responses, resp)
			}

		case <-t.C:
			// update completed downloads
			for i, resp := range responses {
				if resp != nil && resp.IsComplete() {
					// print final result
					if resp.Err() != nil {
						fmt.Fprintf(os.Stderr, "Error downloading %s: %v\n", resp.Request.URL(), resp.Err())
					} else {
						fmt.Printf("Finished %s %d / %d bytes (%d%%)\n", resp.Filename, resp.BytesComplete(), resp.Size, int(100*resp.Progress()))
					}
					// mark completed
					responses[i] = nil
					completed++
				}
			}

			// update downloads in progress
			inProgress = 0
			for _, resp := range responses {
				if resp != nil {
					inProgress++
					fmt.Printf("Downloading %s %d / %d bytes (%d%%)\033[K\n", resp.Filename, resp.BytesComplete(), resp.Size, int(100*resp.Progress()))
				}
			}
		}
	}
	t.Stop()
	return nil
}
