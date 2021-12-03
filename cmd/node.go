package main

import (
	"bufio"
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type volumeInfo struct {
	MountPath      string
	StageHookURL   *url.URL
	UnstageHookURL *url.URL
}
type nodeServer struct {
	csi.UnimplementedNodeServer
	id      string
	m       sync.Mutex
	state   *state
	volumes map[string]*volumeInfo
}

func newNodeServer(id string, stateFile string) (*nodeServer, error) {
	state, err := readState(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open state file: %w", err)
	}

	return &nodeServer{
		id:      id,
		state:   state,
		volumes: make(map[string]*volumeInfo),
	}, nil
}

func (ns *nodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	return &csi.NodeGetInfoResponse{NodeId: ns.id}, nil
}

func (ns *nodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
					},
				},
			},
		},
	}, nil
}

func (ns *nodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	volumeId := req.GetVolumeId()
	stage := req.GetStagingTargetPath()

	if volumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeId missing in request")
	}
	if stage == "" {
		return nil, status.Error(codes.InvalidArgument, "StagingTargetPath missing in request")
	}

	volCap, err := readVolumeCapability(req.GetVolumeCapability())
	if err != nil {
		return nil, err
	}
	volCtx, err := readVolumeContext(req.GetVolumeContext())
	if err != nil {
		return nil, err
	}

	ns.m.Lock()
	defer ns.m.Unlock()

	if us, ok := ns.state.Volumes[volumeId]; ok {
		if us.StagingPath == stage {
			return &csi.NodeStageVolumeResponse{}, nil
		} else {
			return nil, status.Error(codes.InvalidArgument, "volume is already staged to another StagingTargetPath")
		}
	}

	if _, ok := ns.volumes[stage]; ok {
		return nil, status.Error(codes.InvalidArgument, "another volume is already staged to requested StagingTargetPath")
	}

	if err := os.MkdirAll(stage, 0750); err != nil {
		log.Error().Err(err).Msg("failed to mkdir StagingTargetPath")
		return nil, status.Error(codes.Internal, "failed to mkdir StagingTargetPath")
	}

	if volCap.chmod {
		if err := os.Chmod(stage, volCap.mode); err != nil {
			log.Error().Err(err).Msg("failed to chmod StagingTargetPath")
			return nil, status.Error(codes.Internal, "failed to chmod StagingTargetPath")
		}
	}

	state := volumeState{
		MountPath:   volCtx.mountPath,
		StagingPath: stage,

		StageHookURL:   volCtx.stageHookURL,
		UnstageHookURL: volCtx.unstageHookURL,

		Variables: volCtx.vars,
	}

	if err := ns.runVolume(volumeId, state, false); err != nil {
		log.Error().Err(err).Msg("failed to run Volume")
		return nil, status.Error(codes.Internal, err.Error())
	}

	ns.state.Volumes[volumeId] = state
	ns.state.save()

	return &csi.NodeStageVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	volumeId := req.GetVolumeId()
	stage := req.GetStagingTargetPath()

	if volumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeId missing in request")
	}
	if stage == "" {
		return nil, status.Error(codes.InvalidArgument, "StagingTargetPath missing in request")
	}

	ns.m.Lock()
	defer ns.m.Unlock()

	if vs, ok := ns.state.Volumes[volumeId]; !(ok && vs.StagingPath == stage) {
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	if vi := ns.volumes[stage]; vi != nil {
		if addr := vi.UnstageHookURL.String(); addr != "" {
			resp, err := callHookURL(addr)
			if err != nil {
				log.Error().Err(err).Str("unstageHookURL", addr).Msg("failed to call hook")
				return nil, status.Error(codes.Internal, err.Error())
			}

			log.Info().Str("response", resp).Str("unstageHookURL", addr).Msg("sucessfully called hook")
		}
	}

	if err := os.RemoveAll(stage); err != nil {
		log.Error().Err(err).Msg("failed to remove StagingTargetDir")
		return nil, status.Error(codes.Internal, "failed to remove StagingTargetPath")
	}
	delete(ns.state.Volumes, volumeId)
	ns.state.save()

	return &csi.NodeUnstageVolumeResponse{}, nil
}

func (ns *nodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	volumeId := req.GetVolumeId()
	target := req.GetTargetPath()
	stage := req.GetStagingTargetPath()

	if volumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeId missing in request")
	}
	if target == "" {
		return nil, status.Error(codes.InvalidArgument, "TargetPath missing in request")
	}
	if req.GetVolumeCapability() == nil {
		return nil, status.Error(codes.InvalidArgument, "VolumeCapability missing in request")
	}
	if stage == "" {
		return nil, status.Error(codes.FailedPrecondition, "StagingTargetPath missing in request")
	}

	ns.m.Lock()
	defer ns.m.Unlock()

	vs, ok := ns.state.Volumes[volumeId]
	if !ok {
		return nil, status.Error(codes.NotFound, "unknown VolumeId")
	} else if vs.StagingPath != stage {
		return nil, status.Error(codes.InvalidArgument, "incompatible VolumeId and StagingTargetPath")
	}

	if source, err := getMountSource(vs.MountPath); err != nil {
		log.Error().Err(err).Msg("failed to read mountinfo")
		return nil, status.Error(codes.Internal, "failed to read mountinfo")
	} else if source != "" {
		if source == vs.MountPath {
			return &csi.NodePublishVolumeResponse{}, nil
		} else {
			return nil, status.Error(codes.InvalidArgument, "incompatible StagingTargetPath")
		}
	}

	if err := os.MkdirAll(target, 0750); err != nil {
		log.Error().Err(err).Msg("failed to mkdir")
		return nil, status.Error(codes.Internal, "failed to mkdir TargetPath")
	}

	if err := syscall.Mount(vs.MountPath, target, "", syscall.MS_MGC_VAL|syscall.MS_BIND|syscall.MS_RDONLY, ""); err != nil {
		log.Error().Err(err).Msg("failed to mount")
		return nil, status.Error(codes.Internal, "failed to mount")
	}

	if err := syscall.Mount(vs.MountPath, target, "", syscall.MS_MGC_VAL|syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, ""); err != nil {
		log.Error().Err(err).Msg("failed to remount")
		return nil, status.Error(codes.Internal, "failed to remount")
	}

	return &csi.NodePublishVolumeResponse{}, nil
}

func (ns *nodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	volumeId := req.GetVolumeId()
	target := req.GetTargetPath()

	if volumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "VolumeId missing in request")
	}
	if target == "" {
		return nil, status.Error(codes.InvalidArgument, "TargetPath missing in request")
	}

	ns.m.Lock()
	defer ns.m.Unlock()

	if err := syscall.Unmount(target, 0); err != nil && err != syscall.EINVAL && err != syscall.ENOENT {
		log.Error().Err(err).Msg("failed to unmount")
		return nil, status.Error(codes.Internal, "failed to unmount")
	}

	if err := os.RemoveAll(target); err != nil {
		log.Error().Err(err).Msg("failed to remove TargetPath")
		return nil, status.Error(codes.Internal, "failed to remove TargetPath")
	}

	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (ns *nodeServer) runVolume(volumeId string, state volumeState, restore bool) error {
	if addr := state.StageHookURL.String(); addr != "" {
		resp, err := callHookURL(addr)
		if err != nil {
			log.Error().Err(err).Str("stageHookURL", addr).Msg("failed to call hook")
			return err
		}

		log.Info().Str("response", resp).Str("stageHookURL", addr).Msg("successfully called hook")
	}

	vi := &volumeInfo{
		MountPath:      state.MountPath,
		StageHookURL:   state.StageHookURL,
		UnstageHookURL: state.UnstageHookURL,
	}
	ns.volumes[state.StagingPath] = vi

	return nil
}

func (ns *nodeServer) start() {
	ns.m.Lock()
	defer ns.m.Unlock()

	for volumeId, state := range ns.state.Volumes {
		_, err := os.Stat(state.StagingPath)
		if err != nil {
			continue
		}

		ns.runVolume(volumeId, state, true)
	}
}

func (ns *nodeServer) stop() {
	ns.m.Lock()
	defer ns.m.Unlock()

	for _, vi := range ns.volumes {
		if addr := vi.UnstageHookURL.String(); addr != "" {
			resp, err := callHookURL(addr)
			if err != nil {
				log.Error().Err(err).Str("unstageHookURL", addr).Msg("failed to call hook")
			}
			log.Info().Str("response", resp).Str("unstageHookURL", addr).Msg("successfully called hook")
		}
	}
}

func getMountSource(target string) (string, error) {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return "", err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Split(s.Text(), " ")
		if len(fields) < 5 {
			continue
		}
		if fields[4] == target {
			return fields[3], nil
		}
	}
	return "", s.Err()
}
