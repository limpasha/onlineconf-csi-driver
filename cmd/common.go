package main

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var sanityTest bool

type volumeCapability struct {
	chmod bool
	mode  os.FileMode
}

func readVolumeCapability(capability *csi.VolumeCapability) (*volumeCapability, error) {
	if capability == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability missing in request")
	}

	if mode := capability.GetAccessMode().GetMode(); mode != csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY &&
		mode != csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY &&
		!(sanityTest && mode == csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER) {
		return nil, status.Error(codes.InvalidArgument, "unsupported access mode")
	}

	mount := capability.GetMount()
	if mount == nil {
		return nil, status.Error(codes.InvalidArgument, "AccessType must be mount")
	}
	if mount.GetFsType() != "" && mount.GetFsType() != "ext4" { // ext4 is set by external-provisioner
		return nil, status.Error(codes.InvalidArgument, "unsupported filesystem type")
	}

	cap := &volumeCapability{}
	for _, flag := range mount.GetMountFlags() {
		if strings.HasPrefix(flag, "mode=") {
			val, err := strconv.ParseUint(flag[5:], 8, 12)
			if err != nil {
				log.Error().Err(err).Msg("failed to parse mode")
				return nil, status.Error(codes.InvalidArgument, "invalid mount flags")
			}
			cap.chmod = true
			cap.mode = os.FileMode(val)
		}
	}
	return cap, nil
}

type volumeContext struct {
	vars map[string]string

	stageHookURL   *url.URL
	unstageHookURL *url.URL
}

func readVolumeContext(raw map[string]string) (*volumeContext, error) {
	ctx := &volumeContext{}

	var err error

	if ctx.stageHookURL, err = url.Parse(raw["stageHookURL"]); err != nil || !ctx.stageHookURL.IsAbs() {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("stageHookURL invalid value: %v", err))
	}
	if ctx.unstageHookURL, err = url.Parse(raw["unstageHookURL"]); err != nil || !ctx.unstageHookURL.IsAbs() {
		return nil, status.Error(codes.InvalidArgument, fmt.Sprintf("unstageHookURL invalid value: %v", err))
	}

	for k, v := range raw {
		if strings.HasPrefix(k, "${") && strings.HasSuffix(k, "}") {
			ctx.vars[k[2:len(k)-1]] = v
		}
	}
	return ctx, nil
}

func (volCtx *volumeContext) volumeContext() map[string]string {
	volumeContext := map[string]string{}
	for k, v := range volCtx.vars {
		volumeContext["${"+k+"}"] = v
	}
	return volumeContext
}
