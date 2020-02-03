package api

import (
	"context"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/nicolaferraro/snap/pkg/installer"
	"github.com/nicolaferraro/snap/pkg/language"
	"github.com/nicolaferraro/snap/pkg/language/java"
	"github.com/nicolaferraro/snap/pkg/publisher"
	"github.com/pkg/errors"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultBucketName = "snap"

	DefaultTimeout = 10 * time.Minute
)

type Snap struct {
	languageModule  language.Bindings
	installerModule *installer.Installer
	publisherModule *publisher.Publisher

	namespace string
	direct    bool

	options SnapOptions
}

type SnapOptions struct {
	Bucket string

	Timeout time.Duration

	StdOut io.Writer
	StdErr io.Writer
}

func NewSnap(config *rest.Config, namespace string, direct bool, options SnapOptions) (*Snap, error) {
	if options.Bucket == "" {
		options.Bucket = DefaultBucketName
	}

	if options.Timeout <= 0 {
		options.Timeout = DefaultTimeout
	}

	if options.StdOut == nil {
		options.StdOut = ioutil.Discard
	}
	if options.StdErr == nil {
		options.StdErr = ioutil.Discard
	}

	client, err := ctrl.New(config, ctrl.Options{})
	if err != nil {
		return nil, err
	}
	return &Snap{
		languageModule:  java.NewJavaBindings(options.StdOut, options.StdErr),
		installerModule: installer.NewInstaller(config, client, options.StdOut, options.StdErr),
		publisherModule: publisher.NewPublisher(),

		namespace: namespace,
		direct:    direct,

		options: options,
	}, nil
}

func (s *Snap) Deploy(ctx context.Context, libraryDir string) (string, error) {
	deployCtx, cancel := context.WithTimeout(ctx, s.options.Timeout)
	defer cancel()

	id, err := s.languageModule.GetID(libraryDir)
	if err != nil {
		return id, err
	}

	// ensure installation
	if err := s.Install(deployCtx); err != nil {
		return id, err
	}

	dir, err := ioutil.TempDir("", "snap-")
	if err != nil {
		return id, errors.Wrap(err, "cannot create a temporary dir")
	}
	defer os.RemoveAll(dir)

	if err := s.languageModule.Deploy(libraryDir, dir); err != nil {
		return id, errors.Wrap(err, "error while creating deployment for source code")
	}

	host, err := s.installerModule.OpenConnection(deployCtx, s.namespace, s.direct)
	if err != nil {
		return id, err
	}

	publishDestination := publisher.NewPublishDestination(host, "minio", "minio123", false)

	if err := s.publisherModule.Publish(dir, s.options.Bucket, publishDestination); err != nil {
		return id, errors.Wrap(err, "cannot publish to server")
	}

	return id, nil
}

func (s *Snap) Install(ctx context.Context) error {
	if err := s.installerModule.EnsureInstalled(ctx, s.namespace); err != nil {
		return err
	}
	return nil
}
