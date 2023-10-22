package pkgio

import (
	"bytes"
	"html/template"
	"path/filepath"
	"strings"

	kformpkgmetav1alpha1 "github.com/henderiw-nephio/kform/tools/apis/kform/pkg/meta/v1alpha1"
	"github.com/henderiw-nephio/kform/tools/pkg/fsys"
	"github.com/henderiw-nephio/kform/tools/pkg/pkgio/ignore"
	ko "github.com/nephio-project/nephio/krm-functions/lib/kubeobject"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type PkgInitReadWriter interface {
	Reader
	Writer
}

func NewPkgInitReadWriter(path string, pkgKind kformpkgmetav1alpha1.PkgKind) PkgInitReadWriter {

	// TBD do we add validation here
	// Ignore file processing should be done here
	fs := fsys.NewDiskFS(path)
	ignoreRules := ignore.Empty("")
	return &pkgInitReadWriter{
		reader: &pkgReader{
			fsys:           fs,
			matchFilesGlob: []string{IgnoreFileMatch[0], ReadmeFileMatch[0], PkgFileMatch[0]},
			// no ignore rules are required for init
			ignoreRules: ignoreRules,
		},
		writer: &pkgInitWriter{
			fsys:          fs,
			rootPath:      path,
			parentPkgPath: filepath.Dir(path),
			pkgName:       filepath.Base(path),
			pkgKind:       pkgKind,
		},
	}
}

type pkgInitReadWriter struct {
	reader *pkgReader
	writer *pkgInitWriter
}

func (r *pkgInitReadWriter) Read(data *Data) (*Data, error) {
	return r.reader.Read(data)
}

func (r *pkgInitReadWriter) Write(data *Data) error {
	return r.writer.Write(data)
}

type pkgInitWriter struct {
	fsys          fsys.FS
	rootPath      string
	parentPkgPath string
	pkgName       string
	pkgKind       kformpkgmetav1alpha1.PkgKind
}

func (r *pkgInitWriter) Write(data *Data) error {
	filesToWrite := map[string]func() error{
		ReadmeFileMatch[0]: r.WriteReadmeFile,
		PkgFileMatch[0]:    r.WriteKformFile,
		IgnoreFileMatch[0]: r.WriteIgnoreFile,
	}
	// if the file already exists we dont need to write it
	for fileName := range data.Get() {
		delete(filesToWrite, fileName)
	}
	// write files that dont exist
	for _, writeFn := range filesToWrite {
		if err := writeFn(); err != nil {
			return err
		}
	}
	return nil
}

func (r *pkgInitWriter) WriteKformFile() error {
	kf := kformpkgmetav1alpha1.BuildKptFile(
		metav1.ObjectMeta{Name: r.pkgName},
		kformpkgmetav1alpha1.KformFileSpec{
			Kind: r.pkgKind,
		},
	)
	koe, err := ko.NewFromGoStruct(kf)
	if err != nil {
		return err
	}
	return r.fsys.WriteFile(PkgFileMatch[0], []byte(koe.String()))
}

func (r *pkgInitWriter) WriteReadmeFile() error {
	buff := &bytes.Buffer{}
	t, err := template.New("readme").Parse(readmeTemplate)
	if err != nil {
		return err
	}
	readmeTemplateData := map[string]string{
		"Name":        r.pkgName,
		"Description": r.pkgName,
		"PkgPath":     r.rootPath,
	}
	err = t.Execute(buff, readmeTemplateData)
	if err != nil {
		return err
	}

	// Replace single quotes with backticks.
	b := strings.ReplaceAll(buff.String(), "'", "`")

	return r.fsys.WriteFile(ReadmeFileMatch[0], []byte(b))

}

func (r *pkgInitWriter) WriteIgnoreFile() error {
	return r.fsys.WriteFile(IgnoreFileMatch[0], []byte{})
}

// readmeTemplate is the content for the automatically generated README.md file.
// It uses ' instead of ` since golang doesn't allow using ` in a raw string
// literal. We do a replace on the content before printing.
var readmeTemplate = `# {{.Name}}

## Description
{{.Description}}

## Usage

### View package content
'kform pkg tree {{.PkgPath}}'
Details: https://kform.dev/reference/cli/pkg/tree/

`
