/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package remote

import (
	"time"

	internalapi "github.com/kubernetes-sigs/cri-tools/kubelet/apis/cri"

	runtimeapi "github.com/alibaba/pouch/cri/apis/v1alpha2"
	"github.com/golang/glog"
	"google.golang.org/grpc"
	"k8s.io/kubernetes/pkg/kubelet/util"
)

// RemoteVolumeService is a gRPC implementation of internalapi.VolumeManagerService.
type RemoteVolumeService struct {
	timeout      time.Duration
	volumeClient runtimeapi.VolumeServiceClient
}

// NewRemoteVolumeService creates a new internalapi.VolumeManagerService.
func NewRemoteVolumeService(endpoint string, connectionTimeout time.Duration) (internalapi.VolumeManagerService, error) {
	glog.V(3).Infof("Connecting to volume service %s", endpoint)
	addr, dailer, err := util.GetAddressAndDialer(endpoint)
	if err != nil {
		return nil, err
	}

	conn, err := grpc.Dial(addr, grpc.WithInsecure(), grpc.WithTimeout(connectionTimeout), grpc.WithDialer(dailer), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxMsgSize)))
	if err != nil {
		glog.Errorf("Connect remote volume service %s failed: %v", addr, err)
		return nil, err
	}

	return &RemoteVolumeService{
		timeout:      connectionTimeout,
		volumeClient: runtimeapi.NewVolumeServiceClient(conn),
	}, nil
}

// RemoveVolume removes the volume.
func (r *RemoteVolumeService) RemoveVolume(volumeName string) error {
	ctx, cancel := getContextWithTimeout(r.timeout)
	defer cancel()

	_, err := r.volumeClient.RemoveVolume(ctx, &runtimeapi.RemoveVolumeRequest{
		VolumeName: volumeName,
	})
	if err != nil {
		glog.Errorf("RemoveVolume %q from volume service failed: %v", volumeName, err)
		return err
	}

	return nil
}
