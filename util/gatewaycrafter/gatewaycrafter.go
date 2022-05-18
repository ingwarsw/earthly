package gatewaycrafter

import (
	"encoding/json"
	"fmt"

	"github.com/earthly/earthly/states/image"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

// GatewayCrafter abstracts the logic required to interact with the gateway Build and Export functions
type GatewayCrafter interface {
	AddPushImageEntry(ref gwclient.Reference, refID int, imageName string, shouldPush, insecurePush bool, imageConfig *image.Image, platformStr []byte) (string, error)
}

// MapBasedGatewayCrafter extends the GatewayCrafter but provides an additional method for
// fetching the Ref and metadata maps which must be passed to the gwclient.Export method.
type MapBasedGatewayCrafter interface {
	GatewayCrafter
	GetRefsAndMetadata() (map[string]gwclient.Reference, map[string][]byte)
}

// NewGatewayCrafterForBuild creates a new GatewayCrafter designed to be used within the buildkit Build function;
// it configures earthlyoutput exports into the passed in res instance
func NewGatewayCrafterForBuild(res *gwclient.Result) GatewayCrafter {
	return &gatewayCrafterBase{
		addRef:  res.AddRef,
		addMeta: res.AddMeta,
	}
}

// NewGatewayCrafterForExport creates a new GatewayCrafter designed to be used to populate ref and metadata entries for the buildkit Export function
func NewGatewayCrafterForExport() MapBasedGatewayCrafter {
	gebwm := &gatewayCrafterWithMaps{}
	gebwm.refs = map[string]gwclient.Reference{}
	gebwm.metadata = map[string][]byte{}
	gebwm.addRef = func(k string, ref gwclient.Reference) { gebwm.refs[k] = ref }
	gebwm.addMeta = func(k string, v []byte) { gebwm.metadata[k] = v }
	return gebwm
}

// gatewayCrafterBase exposes a common interface for adding references and metadata to either buildkit's Build or Export functions
type gatewayCrafterBase struct {
	addRef  func(string, gwclient.Reference)
	addMeta func(string, []byte)
}

// AddPushImageEntry adds ref and metadata required to cause an image to be pushed
func (ge *gatewayCrafterBase) AddPushImageEntry(ref gwclient.Reference, refID int, imageName string, shouldPush, insecurePush bool, imageConfig *image.Image, platformStr []byte) (string, error) {
	config, err := json.Marshal(imageConfig)
	if err != nil {
		return "", errors.Wrapf(err, "marshal save image config")
	}

	refKey := fmt.Sprintf("image-%d", refID)
	refPrefix := fmt.Sprintf("ref/%s", refKey)

	ge.addRef(refKey, ref)

	ge.addMeta(refPrefix+"/image.name", []byte(imageName))
	if shouldPush {
		ge.addMeta(refPrefix+"/export-image-push", []byte("true"))
		if insecurePush {
			ge.addMeta(refPrefix+"/insecure-push", []byte("true"))
		}
	}
	ge.addMeta(refPrefix+"/"+exptypes.ExporterImageConfigKey, config)

	if platformStr != nil {
		ge.addMeta(refPrefix+"/platform", []byte(platformStr))
	}
	return refPrefix, nil // TODO once all earthlyoutput-metadata-related code is moved into saveimageutil, change to "return err" only
}

type gatewayCrafterWithMaps struct {
	gatewayCrafterBase
	refs     map[string]gwclient.Reference
	metadata map[string][]byte
}

func (ge *gatewayCrafterWithMaps) GetRefsAndMetadata() (map[string]gwclient.Reference, map[string][]byte) {
	return ge.refs, ge.metadata
}
