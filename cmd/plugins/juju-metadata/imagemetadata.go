// Copyright 2013 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/juju/cmd"
	"github.com/juju/errors"
	"github.com/juju/utils/arch"
	"launchpad.net/gnuflag"

	"github.com/juju/juju/cmd/envcmd"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/environs/config"
	"github.com/juju/juju/environs/configstore"
	"github.com/juju/juju/environs/filestorage"
	"github.com/juju/juju/environs/imagemetadata"
	"github.com/juju/juju/environs/simplestreams"
	"github.com/juju/juju/environs/storage"
)

type imageMetadataCommandBase struct {
	envcmd.EnvCommandBase
}

func (c *imageMetadataCommandBase) prepare(context *cmd.Context, store configstore.Storage) (environs.Environ, error) {
	cfg, err := c.Config(store, nil)
	if err != nil {
		return nil, errors.Annotate(err, "could not get config from store")
	}
	// We are preparing an environment to access parameters needed to access
	// image metadata. We don't need, nor want, credential verification.
	// In most cases, credentials will not be available.
	ctx := envcmd.BootstrapContextNoVerify(context)
	return environs.Prepare(cfg, ctx, store)
}

func newImageMetadataCommand() cmd.Command {
	return envcmd.Wrap(&imageMetadataCommand{})
}

// imageMetadataCommand is used to write out simplestreams image metadata information.
type imageMetadataCommand struct {
	imageMetadataCommandBase
	Dir            string
	Series         string
	Arch           string
	ImageId        string
	Region         string
	Endpoint       string
	Stream         string
	VirtType       string
	Storage        string
	privateStorage string
}

var imageMetadataDoc = `
generate-image creates simplestreams image metadata for the specified cloud.

The cloud specification comes from the current Juju model, as specified in
the usual way from either ~/.juju/models.yaml, the -m option, or JUJU_MODEL.

Using command arguments, it is possible to override cloud attributes region, endpoint, and series.
By default, "amd64" is used for the architecture but this may also be changed.
`

func (c *imageMetadataCommand) Info() *cmd.Info {
	return &cmd.Info{
		Name:    "generate-image",
		Purpose: "generate simplestreams image metadata",
		Doc:     imageMetadataDoc,
	}
}

func (c *imageMetadataCommand) SetFlags(f *gnuflag.FlagSet) {
	f.StringVar(&c.Series, "s", "", "the charm series")
	f.StringVar(&c.Arch, "a", arch.AMD64, "the image achitecture")
	f.StringVar(&c.Dir, "d", "", "the destination directory in which to place the metadata files")
	f.StringVar(&c.ImageId, "i", "", "the image id")
	f.StringVar(&c.Region, "r", "", "the region")
	f.StringVar(&c.Endpoint, "u", "", "the cloud endpoint (for Openstack, this is the Identity Service endpoint)")
	f.StringVar(&c.Stream, "stream", imagemetadata.ReleasedStream, "the image stream")
	f.StringVar(&c.VirtType, "virt-type", "", "the image virtualisation type")
	f.StringVar(&c.Storage, "storage", "", "the type of root storage")
}

// setParams sets parameters based on the environment configuration
// for those which have not been explicitly specified.
func (c *imageMetadataCommand) setParams(context *cmd.Context) error {
	c.privateStorage = "<private storage name>"
	var environ environs.Environ
	if store, err := configstore.Default(); err == nil {
		if environ, err = c.prepare(context, store); err == nil {
			logger.Infof("creating image metadata for model %q", environ.Config().Name())
			// If the user has not specified region and endpoint, try and get it from the environment.
			if c.Region == "" || c.Endpoint == "" {
				var cloudSpec simplestreams.CloudSpec
				if inst, ok := environ.(simplestreams.HasRegion); ok {
					if cloudSpec, err = inst.Region(); err != nil {
						return err
					}
				} else {
					return errors.Errorf("model %q cannot provide region and endpoint", environ.Config().Name())
				}
				// If only one of region or endpoint is provided, that is a problem.
				if cloudSpec.Region != cloudSpec.Endpoint && (cloudSpec.Region == "" || cloudSpec.Endpoint == "") {
					return errors.Errorf("cannot generate metadata without a complete cloud configuration")
				}
				if c.Region == "" {
					c.Region = cloudSpec.Region
				}
				if c.Endpoint == "" {
					c.Endpoint = cloudSpec.Endpoint
				}
			}
			cfg := environ.Config()
			if c.Series == "" {
				c.Series = config.PreferredSeries(cfg)
			}
			if v, ok := cfg.AllAttrs()["control-bucket"]; ok {
				c.privateStorage = v.(string)
			}
		} else {
			logger.Warningf("model could not be opened: %v", err)
		}
	}
	if environ == nil {
		logger.Infof("no model found, creating image metadata using user supplied data")
	}
	if c.Series == "" {
		c.Series = config.LatestLtsSeries()
	}
	if c.ImageId == "" {
		return errors.Errorf("image id must be specified")
	}
	if c.Region == "" {
		return errors.Errorf("image region must be specified")
	}
	if c.Endpoint == "" {
		return errors.Errorf("cloud endpoint URL must be specified")
	}
	if c.Dir == "" {
		logger.Infof("no destination directory specified, using current directory")
		var err error
		if c.Dir, err = os.Getwd(); err != nil {
			return err
		}
	}
	return nil
}

var helpDoc = `
Image metadata files have been written to:
%s.
For Juju to use this metadata, the files need to be put into the
image metadata search path. There are 2 options:

1. Use the --metadata-source parameter when bootstrapping:
   juju bootstrap --metadata-source %s

2. Use image-metadata-url in $JUJU_HOME/models.yaml
Configure a http server to serve the contents of
%s
and set the value of image-metadata-url accordingly.
`

func (c *imageMetadataCommand) Run(context *cmd.Context) error {
	if err := c.setParams(context); err != nil {
		return err
	}
	out := context.Stdout
	im := &imagemetadata.ImageMetadata{
		Id:       c.ImageId,
		Arch:     c.Arch,
		Stream:   c.Stream,
		VirtType: c.VirtType,
		Storage:  c.Storage,
	}
	cloudSpec := simplestreams.CloudSpec{
		Region:   c.Region,
		Endpoint: c.Endpoint,
	}
	targetStorage, err := filestorage.NewFileStorageWriter(c.Dir)
	if err != nil {
		return err
	}
	err = imagemetadata.MergeAndWriteMetadata(c.Series, []*imagemetadata.ImageMetadata{im}, &cloudSpec, targetStorage)
	if err != nil {
		return fmt.Errorf("image metadata files could not be created: %v", err)
	}
	dir := context.AbsPath(c.Dir)
	dest := filepath.Join(dir, storage.BaseImagesPath, "streams", "v1")
	fmt.Fprintf(out, fmt.Sprintf(helpDoc, dest, dir, dir))
	return nil
}
