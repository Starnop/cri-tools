/*
Copyright 2017 The Kubernetes Authors.

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

package main

import (
	"fmt"

	pb "github.com/alibaba/pouch/cri/apis/v1alpha2"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
	"golang.org/x/net/context"
)

var removeVolumeCommand = cli.Command{
	Name:      "rmv",
	Usage:     "Remove one volume",
	ArgsUsage: "VOLUME-NAME [VOLUME-NAME...]",
	Action: func(context *cli.Context) error {
		if context.NArg() == 0 {
			return cli.ShowSubcommandHelp(context)
		}
		if err := getVolumeClient(context); err != nil {
			return err
		}
		for i := 0; i < context.NArg(); i++ {
			id := context.Args().Get(i)
			_, err := RemoveVolume(volumeClient, id)
			if err != nil {
				return fmt.Errorf("error of removing image %q: %v", id, err)
			}
		}
		return nil
	},
}

// RemoveVolume sends a RemoveVolumeRequest to the server, and parses
// the returned RemoveVolumeResponse.
func RemoveVolume(client pb.VolumeServiceClient, volume string) (resp *pb.RemoveVolumeResponse, err error) {
	if volume == "" {
		return nil, fmt.Errorf("VolumeName cannot be empty")
	}
	request := &pb.RemoveVolumeRequest{VolumeName: volume}
	logrus.Debugf("RemoveVolumeRequest: %v", request)
	resp, err = client.RemoveVolume(context.Background(), request)
	logrus.Debugf("RemoveVolumeResponse: %v", resp)
	return
}
