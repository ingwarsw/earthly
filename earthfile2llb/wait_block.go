package earthfile2llb

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/earthly/earthly/states"
	"github.com/earthly/earthly/util/llbutil"
	"github.com/earthly/earthly/util/llbutil/pllb"
	"github.com/earthly/earthly/util/syncutil/serrgroup"

	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	"github.com/moby/buildkit/frontend/gateway/client"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/pkg/errors"
)

type saveImageWaitItem struct {
	c    *Converter
	si   states.SaveImage
	push bool
}

type runCommandWaitItem struct {
	c   *Converter
	cmd *pllb.State
}

// waitItem should be either saveImageWaitItem or runCommandWaitItem
type waitItem interface {
}

type waitBlock struct {
	items []waitItem
	mu    sync.Mutex
}

func newWaitBlock() *waitBlock {
	return &waitBlock{}
}

func (wb *waitBlock) addSaveImage(si states.SaveImage, c *Converter, push bool) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	item := saveImageWaitItem{
		c:    c,
		si:   si,
		push: push,
	}
	wb.items = append(wb.items, &item)
}

func (wb *waitBlock) addCommand(cmd *pllb.State, c *Converter) {
	wb.mu.Lock()
	defer wb.mu.Unlock()
	item := runCommandWaitItem{
		c:   c,
		cmd: cmd,
	}
	wb.items = append(wb.items, &item)
}

func (wb *waitBlock) wait(ctx context.Context) error {
	wb.mu.Lock()
	defer wb.mu.Unlock()

	errGroup, ctx := serrgroup.WithContext(ctx)
	errGroup.Go(func() error {
		return wb.saveImages(ctx)
	})
	errGroup.Go(func() error {
		return wb.waitCommands(ctx)
	})
	return errGroup.Wait()
}

func (wb *waitBlock) saveImages(ctx context.Context) error {
	isMultiPlatform := make(map[string]bool)    // DockerTag -> bool
	noManifestListImgs := make(map[string]bool) // DockerTag -> bool
	platformImgNames := make(map[string]bool)

	imageWaitItems := []*saveImageWaitItem{}
	for _, item := range wb.items {
		saveImage, ok := item.(*saveImageWaitItem)
		if !ok {
			continue
		}
		if saveImage.si.NoManifestList {
			noManifestListImgs[saveImage.si.DockerTag] = true
		} else {
			isMultiPlatform[saveImage.si.DockerTag] = true // do I need to count for previsouly seen?
		}
		if isMultiPlatform[saveImage.si.DockerTag] && noManifestListImgs[saveImage.si.DockerTag] {
			return fmt.Errorf("cannot save image %s defined multiple times, but declared as SAVE IMAGE --no-manifest-list", saveImage.si.DockerTag)
		}
		imageWaitItems = append(imageWaitItems, saveImage)
	}
	if len(imageWaitItems) == 0 {
		return nil
	}

	metadata := map[string][]byte{}
	refs := map[string]client.Reference{}

	refID := 0
	for _, item := range imageWaitItems {
		if !item.push {
			continue
		}

		ref, err := llbutil.StateToRef(
			ctx, item.c.opt.GwClient, item.si.State, item.c.opt.NoCache,
			item.c.platr, item.c.opt.CacheImports.AsMap())
		if err != nil {
			return errors.Wrapf(err, "failed to solve image required for %s", item.si.DockerTag)
		}

		config, err := json.Marshal(item.si.Image)
		if err != nil {
			return errors.Wrapf(err, "marshal save image config")
		}

		refKey := fmt.Sprintf("image-%d", refID)
		refPrefix := fmt.Sprintf("ref/%s", refKey)
		refs[refKey] = ref

		metadata[refPrefix+"/image.name"] = []byte(item.si.DockerTag)
		metadata[refPrefix+"/export-image-push"] = []byte("true")
		if item.si.InsecurePush {
			metadata[refPrefix+"/insecure-push"] = []byte("true")
		}
		metadata[refPrefix+"/"+exptypes.ExporterImageConfigKey] = config
		refID++

		if isMultiPlatform[item.si.DockerTag] {
			platformStr := item.si.Platform.String()
			platformImgName, err := llbutil.PlatformSpecificImageName(item.si.DockerTag, item.si.Platform)
			if err != nil {
				return err
			}

			if item.si.CheckDuplicate && item.si.DockerTag != "" {
				if _, found := platformImgNames[platformImgName]; found {
					return errors.Errorf(
						"image %s is defined multiple times for the same platform (%s)",
						item.si.DockerTag, platformImgName)
				}
				platformImgNames[platformImgName] = true
			}

			metadata[refPrefix+"/platform"] = []byte(platformStr)
		}
	}

	if len(imageWaitItems) == 0 {
		panic("saveImagesWaitItem should never have been created with zero converters")
	}
	gatewayClient := imageWaitItems[0].c.opt.GwClient // could be any converter's gwClient (they should app be the same)

	err := gatewayClient.Export(ctx, gwclient.ExportRequest{
		Refs:     refs,
		Metadata: metadata,
	})
	if err != nil {
		return errors.Wrap(err, "failed to SAVE IMAGE")
	}
	return nil
}

func (wb *waitBlock) waitCommands(ctx context.Context) error {
	errGroup, ctx := serrgroup.WithContext(ctx)

	for _, item := range wb.items {
		cmdItem, ok := item.(*runCommandWaitItem)
		if !ok {
			continue
		}
		errGroup.Go(func() error {
			return cmdItem.c.forceExecution(ctx, *cmdItem.cmd, cmdItem.c.platr)
		})
	}
	return errGroup.Wait()
}
