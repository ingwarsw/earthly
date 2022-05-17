package saveimageutil

import (
	"encoding/json"
	"fmt"

	"github.com/earthly/earthly/states/image"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

// NewBuildImageSaver creates a new ImageSaver designed to be used within the buildkit Build function
func NewBuildImageSaver(res *gwclient.Result) *ImageSaver {
	return &ImageSaver{
		addRef:  res.AddRef,
		addMeta: res.AddMeta,
	}
}

// NewExportImageSaver creates a new ImageSaver designed to be used to populate ref and metadata entries for the buildkit Export function
func NewExportImageSaver(refs map[string]gwclient.Reference, metadata map[string][]byte) *ImageSaver {
	return &ImageSaver{
		addRef:  func(k string, ref gwclient.Reference) { refs[k] = ref },
		addMeta: func(k string, v []byte) { metadata[k] = v },
	}
}

// ImageSaver exposes a common interface for adding references and metadata to either buildkit's Build or Export functions
type ImageSaver struct {
	addRef  func(string, gwclient.Reference)
	addMeta func(string, []byte)
}

// AddPushImageEntry adds ref and metadata required to cause an image to be pushed
func (is *ImageSaver) AddPushImageEntry(ref gwclient.Reference, refID int, imageName string, shouldPush, insecurePush bool, imageConfig *image.Image, platformStr []byte) (string, error) {
	config, err := json.Marshal(imageConfig)
	if err != nil {
		return "", errors.Wrapf(err, "marshal save image config")
	}

	refKey := fmt.Sprintf("image-%d", refID)
	refPrefix := fmt.Sprintf("ref/%s", refKey)

	is.addRef(refKey, ref)

	is.addMeta(refPrefix+"/image.name", []byte(imageName))
	if shouldPush {
		is.addMeta(refPrefix+"/export-image-push", []byte("true"))
		if insecurePush {
			is.addMeta(refPrefix+"/insecure-push", []byte("true"))
		}
	}
	is.addMeta(refPrefix+"/"+exptypes.ExporterImageConfigKey, config)

	if platformStr != nil {
		is.addMeta(refPrefix+"/platform", []byte(platformStr))
	}
	return refPrefix, nil // TODO once all earthlyoutput-metadata-related code is moved into saveimageutil, change to "return err" only
}
