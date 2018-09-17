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

package validate

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	internalapi "github.com/kubernetes-sigs/cri-tools/kubelet/apis/cri"
	"github.com/kubernetes-sigs/cri-tools/pkg/framework"

	runtimeapi "github.com/alibaba/pouch/cri/apis/v1alpha2"
	"github.com/docker/docker/pkg/jsonlog"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

// streamType is the type of the stream.
type streamType string

const (
	defaultStopContainerTimeout int64      = 60
	defaultExecSyncTimeout      int64      = 5
	defaultLog                  string     = "hello World"
	stdoutType                  streamType = "stdout"
	stderrType                  streamType = "stderr"
)

// logMessage is the internal log type.
type logMessage struct {
	timestamp time.Time
	stream    streamType
	log       string
}

var _ = framework.KubeDescribe("Container", func() {
	f := framework.NewDefaultCRIFramework()

	var rc internalapi.RuntimeService
	var ic internalapi.ImageManagerService
	var vc internalapi.VolumeManagerService

	BeforeEach(func() {
		rc = f.CRIClient.CRIRuntimeClient
		ic = f.CRIClient.CRIImageClient
		vc = f.CRIClient.CRIVolumeClient
	})

	Context("runtime should support basic operations on container", func() {
		var podID string
		var podConfig *runtimeapi.PodSandboxConfig

		BeforeEach(func() {
			podID, podConfig = framework.CreatePodSandboxForContainer(rc)
		})

		AfterEach(func() {
			By("stop PodSandbox")
			rc.StopPodSandbox(podID)
			By("delete PodSandbox")
			rc.RemovePodSandbox(podID)
		})

		It("runtime should support creating container [Conformance]", func() {
			By("test create a default container")
			containerID := testCreateDefaultContainer(rc, ic, podID, podConfig)

			By("test list container")
			containers := listContainerForID(rc, containerID)
			Expect(containerFound(containers, containerID)).To(BeTrue(), "Container should be created")
		})

		It("runtime should support starting container [Conformance]", func() {
			By("create container")
			containerID := framework.CreateDefaultContainer(rc, ic, podID, podConfig, "container-for-start-test-")

			By("test start container")
			testStartContainer(rc, containerID)
		})

		It("runtime should support stopping container [Conformance]", func() {
			By("create container")
			containerID := framework.CreateDefaultContainer(rc, ic, podID, podConfig, "container-for-stop-test-")

			By("start container")
			startContainer(rc, containerID)

			By("test stop container")
			testStopContainer(rc, containerID)
		})

		It("runtime should support removing container [Conformance]", func() {
			By("create container")
			containerID := framework.CreateDefaultContainer(rc, ic, podID, podConfig, "container-for-remove-test-")

			By("test remove container")
			removeContainer(rc, containerID)
			containers := listContainerForID(rc, containerID)
			Expect(containerFound(containers, containerID)).To(BeFalse(), "Container should be removed")
		})

		It("runtime should support execSync [Conformance]", func() {
			By("create container")
			containerID := framework.CreateDefaultContainer(rc, ic, podID, podConfig, "container-for-execSync-test-")

			By("start container")
			startContainer(rc, containerID)

			By("test execSync")
			cmd := []string{"echo", "hello"}
			expectedLogMessage := "hello\n"
			verifyExecSyncOutput(rc, containerID, cmd, expectedLogMessage)
		})

		It("runtime should support execSync with wrong command [Conformance]", func() {
			By("create container")
			containerID := framework.CreateDefaultContainer(rc, ic, podID, podConfig, "container-for-execSync-test-")

			By("start container")
			startContainer(rc, containerID)

			By("test execSync")
			cmd := []string{"not-exist-command"}
			verifyExecSyncContainOutput(rc, containerID, cmd)
		})
	})

	Context("runtime should support adding volume and device", func() {
		var podID string
		var podConfig *runtimeapi.PodSandboxConfig

		BeforeEach(func() {
			podID, podConfig = framework.CreatePodSandboxForContainer(rc)
		})

		AfterEach(func() {
			By("stop PodSandbox")
			rc.StopPodSandbox(podID)
			By("delete PodSandbox")
			rc.RemovePodSandbox(podID)
		})

		It("runtime should support starting container with volume [Conformance]", func() {
			By("create host path and flag file")
			hostPath, _ := createHostPath(podID)

			defer os.RemoveAll(hostPath) // clean up the TempDir

			By("create container with volume")
			containerID := createVolumeContainer(rc, ic, "container-with-volume-test-", podID, podConfig, hostPath)

			By("test start container with volume")
			testStartContainer(rc, containerID)

			By("check whether 'hostPath' contains file or dir in container")
			command := []string{"ls", "-A", hostPath}
			output := execSyncContainer(rc, containerID, command)
			Expect(len(output)).NotTo(BeZero(), "len(output) should not be zero.")
		})

		It("runtime should support starting container with volume when host path is a symlink [Conformance]", func() {
			By("create host path and flag file")
			hostPath, _ := createHostPath(podID)
			defer os.RemoveAll(hostPath) // clean up the TempDir

			By("create symlink")
			symlinkPath := createSymlink(hostPath)
			defer os.RemoveAll(symlinkPath) // clean up the symlink

			By("create volume container with symlink host path")
			containerID := createVolumeContainer(rc, ic, "container-with-symlink-host-path-test-", podID, podConfig, symlinkPath)

			By("test start volume container with symlink host path")
			testStartContainer(rc, containerID)

			By("check whether 'symlink' contains file or dir in container")
			command := []string{"ls", "-A", symlinkPath}
			output := execSyncContainer(rc, containerID, command)
			Expect(len(output)).NotTo(BeZero(), "len(output) should not be zero.")
		})

		// TODO(random-liu): Decide whether to add host path not exist test when https://github.com/kubernetes/kubernetes/pull/61460
		// is finalized.
	})

	Context("runtime should support log", func() {
		var podID, hostPath string
		var podConfig *runtimeapi.PodSandboxConfig

		BeforeEach(func() {
			podID, podConfig, hostPath = createPodSandboxWithLogDirectory(rc)
		})

		AfterEach(func() {
			By("stop PodSandbox")
			rc.StopPodSandbox(podID)
			By("delete PodSandbox")
			rc.RemovePodSandbox(podID)
			By("clean up the TempDir")
			os.RemoveAll(hostPath)
		})

		It("runtime should support starting container with log [Conformance]", func() {
			By("create container with log")
			logPath, containerID := createLogContainer(rc, ic, "container-with-log-test-", podID, podConfig)

			By("start container with log")
			startContainer(rc, containerID)
			// wait container exited and check the status.
			Eventually(func() runtimeapi.ContainerState {
				return getContainerStatus(rc, containerID).State
			}, time.Minute, time.Second*4).Should(Equal(runtimeapi.ContainerState_CONTAINER_EXITED))

			By("check the log context")
			expectedLogMessage := defaultLog + "\n"
			verifyLogContents(podConfig, logPath, expectedLogMessage, stdoutType)
		})

		It("runtime should support reopening container log [Conformance]", func() {
			By("create container with log")
			logPath, containerID := createKeepLoggingContainer(rc, ic, "container-reopen-log-test-", podID, podConfig)

			By("start container with log")
			startContainer(rc, containerID)

			Eventually(func() []logMessage {
				return parseLogLine(podConfig, logPath)
			}, time.Minute, time.Second).ShouldNot(BeEmpty(), "container log should be generated")

			By("rename container log")
			newLogPath := logPath + ".new"
			Expect(os.Rename(filepath.Join(podConfig.LogDirectory, logPath),
				filepath.Join(podConfig.LogDirectory, newLogPath))).To(Succeed())

			By("reopen container log")
			Expect(rc.ReopenContainerLog(containerID)).To(Succeed())

			Expect(pathExists(filepath.Join(podConfig.LogDirectory, logPath))).To(
				BeTrue(), "new container log file should be created")
			Eventually(func() []logMessage {
				return parseLogLine(podConfig, logPath)
			}, time.Minute, time.Second).ShouldNot(BeEmpty(), "new container log should be generated")
			oldLength := len(parseLogLine(podConfig, newLogPath))
			Consistently(func() int {
				return len(parseLogLine(podConfig, newLogPath))
			}, 5*time.Second, time.Second).Should(Equal(oldLength), "old container log should not change")
		})
	})

	Context("runtime should support extensional operations on container", func() {
		var podID string
		var podConfig *runtimeapi.PodSandboxConfig

		BeforeEach(func() {
			podID, podConfig = framework.CreatePodSandboxForContainer(rc)
		})

		AfterEach(func() {
			By("stop PodSandbox")
			rc.StopPodSandbox(podID)
			By("delete PodSandbox")
			rc.RemovePodSandbox(podID)
		})

		It("runtime should support creating extended container [Conformance]", func() {
			By("create host path and flag file")
			hostPath, _ := createHostPath(podID)

			defer os.RemoveAll(hostPath) // clean up the TempDir

			By("test create a extended container")
			containerID := createExtendedContainer(rc, ic, "container-with-extended-test-", podID, podConfig, hostPath)

			By("test list container")
			containers := listContainerForID(rc, containerID)
			Expect(containerFound(containers, containerID)).To(BeTrue(), "Container should be created")
		})

		It("runtime should support starting extended container [Conformance]", func() {
			By("create host path and flag file")
			hostPath, _ := createHostPath(podID)

			defer os.RemoveAll(hostPath) // clean up the TempDir

			By("create a extended container")
			containerID := createExtendedContainer(rc, ic, "container-with-extended-test-", podID, podConfig, hostPath)

			By("start container with extended")
			testStartContainer(rc, containerID)
		})

		It("runtime should support getting container status [Conformance]", func() {
			By("create host path and flag file")
			hostPath, _ := createHostPath(podID)

			defer os.RemoveAll(hostPath) // clean up the TempDir

			By("create a extended container")
			containerID := createExtendedContainer(rc, ic, "container-with-extended-test-", podID, podConfig, hostPath)

			By("get container status")
			containerStatus := getContainerStatus(rc, containerID)
			Expect(containerStatus.GetQuotaId()).NotTo(BeNil(), "The quotaId of container should not be nil.")
			Expect(containerStatus.GetResources()).NotTo(BeNil(), "The resources of container should not be nil.")
			Expect(containerStatus.GetMounts()).NotTo(BeNil(), "The mounts of container should not be nil.")
			Expect(containerStatus.GetEnvs()).NotTo(BeNil(), "The envs of container should not be nil.")
		})

		It("runtime should support getting image status [Conformance]", func() {
			By("create host path and flag file")
			hostPath, _ := createHostPath(podID)

			defer os.RemoveAll(hostPath) // clean up the TempDir

			By("create a extended container")
			containerID := createExtendedContainer(rc, ic, "container-with-extended-test-", podID, podConfig, hostPath)

			By("get image status")
			containerStatus := getContainerStatus(rc, containerID)
			imageStatus := framework.ImageStatus(ic, containerStatus.GetImage().GetImage()).GetVolumes()
			Expect(imageStatus).NotTo(BeNil(), "The volumes of image should not be nil.")
		})

		It("runtime should support removing volumes [Conformance]", func() {
			By("create host path and flag file")
			hostPath, _ := createHostPath(podID)

			defer os.RemoveAll(hostPath) // clean up the TempDir

			By("create a extended container")
			containerID := createExtendedContainer(rc, ic, "container-with-extended-test-", podID, podConfig, hostPath)

			By("Get container mounts for containerID: " + containerID)
			containerStatus := getContainerStatus(rc, containerID)
			mounts := containerStatus.GetMounts()

			By("Get container volumes for containerID: " + containerID)
			volumeName := getContainerVolumeNamed(mounts)
			Expect(volumeName).NotTo(Equal(""), "The volumeName should not be %s", "")

			By("stop container")
			stopContainer(rc, containerID, defaultStopContainerTimeout)

			By("remove container")
			removeContainer(rc, containerID)

			// NOTE removeContainer will remove the relevant volume.
		})
	})

})

// containerFound returns whether containers is found.
func containerFound(containers []*runtimeapi.Container, containerID string) bool {
	for _, container := range containers {
		if container.Id == containerID {
			return true
		}
	}
	return false
}

// mountFound returns whether mounts is found.
func mountFound(mounts []*runtimeapi.Mount, mountName string) bool {
	for _, mount := range mounts {
		if mount.Name == mountName {
			return true
		}
	}
	return false
}

// getContainerVolumeNamed gets getContainerVolumeNamed for containerID and fails if it gets error.
func getContainerVolumeNamed(mounts []*runtimeapi.Mount) string {
	for _, v := range mounts {
		if volumeName := v.GetName(); volumeName != "" {
			return volumeName
		}
	}
	return ""
}

// getContainerStatus gets ContainerState for containerID and fails if it gets error.
func getContainerStatus(c internalapi.RuntimeService, containerID string) *runtimeapi.ContainerStatus {
	By("Get container status for containerID: " + containerID)
	status, err := c.ContainerStatus(containerID)
	framework.ExpectNoError(err, "failed to get container %q status: %v", containerID, err)
	return status
}

// createShellContainer creates a container to run /bin/sh.
func createShellContainer(rc internalapi.RuntimeService, ic internalapi.ImageManagerService, podID string, podConfig *runtimeapi.PodSandboxConfig, prefix string) string {
	containerName := prefix + framework.NewUUID()
	containerConfig := &runtimeapi.ContainerConfig{
		Metadata:  framework.BuildContainerMetadata(containerName, framework.DefaultAttempt),
		Image:     &runtimeapi.ImageSpec{Image: framework.DefaultContainerImage},
		Command:   []string{"/bin/sh"},
		Linux:     &runtimeapi.LinuxContainerConfig{},
		Stdin:     true,
		StdinOnce: true,
		Tty:       false,
	}

	return framework.CreateContainer(rc, ic, containerConfig, podID, podConfig)
}

// testCreateDefaultContainer creates a container in the pod which ID is podID and make sure it's ready.
func testCreateDefaultContainer(rc internalapi.RuntimeService, ic internalapi.ImageManagerService, podID string, podConfig *runtimeapi.PodSandboxConfig) string {
	containerID := framework.CreateDefaultContainer(rc, ic, podID, podConfig, "container-for-create-test-")
	Eventually(func() runtimeapi.ContainerState {
		return getContainerStatus(rc, containerID).State
	}, time.Minute, time.Second*4).Should(Equal(runtimeapi.ContainerState_CONTAINER_CREATED))
	return containerID
}

// startContainer start the container for containerID.
func startContainer(c internalapi.RuntimeService, containerID string) {
	By("Start container for containerID: " + containerID)
	err := c.StartContainer(containerID)
	framework.ExpectNoError(err, "failed to start container: %v", err)
	framework.Logf("Started container %q\n", containerID)
}

// testStartContainer starts the container for containerID and make sure it's running.
func testStartContainer(rc internalapi.RuntimeService, containerID string) {
	startContainer(rc, containerID)
	Eventually(func() runtimeapi.ContainerState {
		return getContainerStatus(rc, containerID).State
	}, time.Minute, time.Second*4).Should(Equal(runtimeapi.ContainerState_CONTAINER_RUNNING))
}

// stopContainer stops the container for containerID.
func stopContainer(c internalapi.RuntimeService, containerID string, timeout int64) {
	By("Stop container for containerID: " + containerID)
	stopped := make(chan bool, 1)

	go func() {
		defer GinkgoRecover()
		err := c.StopContainer(containerID, timeout)
		framework.ExpectNoError(err, "failed to stop container: %v", err)
		stopped <- true
	}()

	select {
	case <-time.After(time.Duration(timeout) * time.Second):
		framework.Failf("stop container %q timeout.\n", containerID)
	case <-stopped:
		framework.Logf("Stopped container %q\n", containerID)
	}
}

// testStopContainer stops the container for containerID and make sure it's exited.
func testStopContainer(c internalapi.RuntimeService, containerID string) {
	stopContainer(c, containerID, defaultStopContainerTimeout)
	Eventually(func() runtimeapi.ContainerState {
		return getContainerStatus(c, containerID).State
	}, time.Minute, time.Second*4).Should(Equal(runtimeapi.ContainerState_CONTAINER_EXITED))
}

// removeContainer removes the container for containerID.
func removeContainer(c internalapi.RuntimeService, containerID string) {
	By("Remove container for containerID: " + containerID)
	err := c.RemoveContainer(containerID)
	framework.ExpectNoError(err, "failed to remove container: %v", err)
	framework.Logf("Removed container %q\n", containerID)
}

// listContainerForID lists container for containerID.
func listContainerForID(c internalapi.RuntimeService, containerID string) []*runtimeapi.Container {
	By("List containers for containerID: " + containerID)
	filter := &runtimeapi.ContainerFilter{
		Id: containerID,
	}
	containers, err := c.ListContainers(filter)
	framework.ExpectNoError(err, "failed to list containers %q status: %v", containerID, err)
	return containers
}

// execSyncContainer test execSync for containerID and make sure the response is right.
func execSyncContainer(c internalapi.RuntimeService, containerID string, command []string) string {
	By("execSync for containerID: " + containerID)
	stdout, stderr, err := c.ExecSync(containerID, command, time.Duration(defaultExecSyncTimeout)*time.Second)
	framework.ExpectNoError(err, "failed to execSync in container %q", containerID)
	Expect(stderr).To(BeNil(), "The stderr should be nil.")
	framework.Logf("Execsync succeed")

	return string(stdout)
}

// execSyncContainer test execSync for containerID and make sure the response is right.
func verifyExecSyncOutput(c internalapi.RuntimeService, containerID string, command []string, expectedLogMessage string) {
	By("verify execSync output")
	stdout := execSyncContainer(c, containerID, command)
	Expect(stdout).To(Equal(expectedLogMessage), "The stdout output of execSync should be %s", expectedLogMessage)
	framework.Logf("verfiy Execsync output succeed")
}

// execSyncContainer test execSync for containerID and make sure the response is right.
func verifyExecSyncContainOutput(c internalapi.RuntimeService, containerID string, command []string) {
	By("verify execSync containOutput")
	_, _, err := c.ExecSync(containerID, command, time.Duration(defaultExecSyncTimeout)*time.Second)
	Expect(err).To(HaveOccurred())
	framework.Logf("verfiy Execsync containOutput succeed")
}

// createHostPath creates the hostPath and flagFile for volume.
func createHostPath(podID string) (string, string) {
	hostPath, err := ioutil.TempDir("", "/test"+podID)
	framework.ExpectNoError(err, "failed to create TempDir %q: %v", hostPath, err)

	flagFile := "testVolume.file"
	_, err = os.Create(filepath.Join(hostPath, flagFile))
	framework.ExpectNoError(err, "failed to create volume file %q: %v", flagFile, err)

	return hostPath, flagFile
}

// createSymlink creates a symlink of path.
func createSymlink(path string) string {
	symlinkPath := path + "-symlink"
	framework.ExpectNoError(os.Symlink(path, symlinkPath), "failed to create symlink %q", symlinkPath)
	return symlinkPath
}

// createVolumeContainer creates a container with volume and the prefix of containerName and fails if it gets error.
func createVolumeContainer(rc internalapi.RuntimeService, ic internalapi.ImageManagerService, prefix string, podID string, podConfig *runtimeapi.PodSandboxConfig, hostPath string) string {
	By("create a container with volume and name")
	containerName := prefix + framework.NewUUID()
	containerConfig := &runtimeapi.ContainerConfig{
		Metadata: framework.BuildContainerMetadata(containerName, framework.DefaultAttempt),
		Image:    &runtimeapi.ImageSpec{Image: framework.DefaultContainerImage},
		Command:  []string{"sh", "-c", "top"},
		// mount host path to the same directory in container, and will check if hostPath isn't empty
		Mounts: []*runtimeapi.Mount{
			{
				HostPath:      hostPath,
				ContainerPath: hostPath,
			},
		},
	}

	return framework.CreateContainer(rc, ic, containerConfig, podID, podConfig)
}

// createLogContainer creates a container with log and the prefix of containerName.
func createLogContainer(rc internalapi.RuntimeService, ic internalapi.ImageManagerService, prefix string, podID string, podConfig *runtimeapi.PodSandboxConfig) (string, string) {
	By("create a container with log and name")
	containerName := prefix + framework.NewUUID()
	path := fmt.Sprintf("%s.log", containerName)
	containerConfig := &runtimeapi.ContainerConfig{
		Metadata: framework.BuildContainerMetadata(containerName, framework.DefaultAttempt),
		Image:    &runtimeapi.ImageSpec{Image: framework.DefaultContainerImage},
		Command:  []string{"echo", defaultLog},
		LogPath:  path,
	}
	return containerConfig.LogPath, framework.CreateContainer(rc, ic, containerConfig, podID, podConfig)
}

// createKeepLoggingContainer creates a container keeps logging defaultLog to output.
func createKeepLoggingContainer(rc internalapi.RuntimeService, ic internalapi.ImageManagerService, prefix string, podID string, podConfig *runtimeapi.PodSandboxConfig) (string, string) {
	By("create a container with log and name")
	containerName := prefix + framework.NewUUID()
	path := fmt.Sprintf("%s.log", containerName)
	containerConfig := &runtimeapi.ContainerConfig{
		Metadata: framework.BuildContainerMetadata(containerName, framework.DefaultAttempt),
		Image:    &runtimeapi.ImageSpec{Image: framework.DefaultContainerImage},
		Command:  []string{"sh", "-c", "while true; do echo " + defaultLog + "; sleep 1; done"},
		LogPath:  path,
	}
	return containerConfig.LogPath, framework.CreateContainer(rc, ic, containerConfig, podID, podConfig)
}

// createExtendedContainer creates a container with fields extended, such as diskquota, quotaId, etc.
func createExtendedContainer(rc internalapi.RuntimeService, ic internalapi.ImageManagerService, prefix string, podID string, podConfig *runtimeapi.PodSandboxConfig, hostPath string) string {
	By("create a container with fields extended")
	containerName := prefix + framework.NewUUID()
	containerConfig := &runtimeapi.ContainerConfig{
		Metadata: framework.BuildContainerMetadata(containerName, framework.DefaultAttempt),
		Image:    &runtimeapi.ImageSpec{Image: framework.DefaultContainerVolumeImage},
		Command:  []string{"sh", "-c", "top"},
		Envs:     []*runtimeapi.KeyValue{{"GO_VERSION", "1.9.1"}},
		Linux: &runtimeapi.LinuxContainerConfig{
			Resources: &runtimeapi.LinuxContainerResources{
				DiskQuota: map[string]string{"/": "10g"},
			},
		},
	}
	return framework.CreateContainer(rc, ic, containerConfig, podID, podConfig)
}

// pathExists check whether 'path' does exist or not
func pathExists(path string) bool {
	_, err := os.Stat(path)
	if err == nil {
		return true
	}
	if os.IsNotExist(err) {
		return false
	}
	framework.ExpectNoError(err, "failed to check whether %q Exists: %v", path, err)
	return false
}

// parseDockerJSONLog parses logs in Docker JSON log format.
// Docker JSON log format example:
//   {"log":"content 1","stream":"stdout","time":"2016-10-20T18:39:20.57606443Z"}
//   {"log":"content 2","stream":"stderr","time":"2016-10-20T18:39:20.57606444Z"}
func parseDockerJSONLog(log []byte, msg *logMessage) {
	var l jsonlog.JSONLog

	err := json.Unmarshal(log, &l)
	framework.ExpectNoError(err, "failed with %v to unmarshal log %q", err, l)

	msg.timestamp = l.Created
	msg.stream = streamType(l.Stream)
	msg.log = l.Log
}

// parseCRILog parses logs in CRI log format.
// CRI log format example :
//   2016-10-06T00:17:09.669794202Z stdout P The content of the log entry 1
//   2016-10-06T00:17:10.113242941Z stderr F The content of the log entry 2
func parseCRILog(log string, msg *logMessage) {
	logMessage := strings.SplitN(log, " ", 4)
	if len(log) < 4 {
		err := errors.New("invalid CRI log")
		framework.ExpectNoError(err, "failed to parse CRI log: %v", err)
	}
	timeStamp, err := time.Parse(time.RFC3339Nano, logMessage[0])
	framework.ExpectNoError(err, "failed to parse timeStamp: %v", err)
	stream := logMessage[1]

	msg.timestamp = timeStamp
	msg.stream = streamType(stream)
	// Skip the tag field.
	msg.log = logMessage[3] + "\n"
}

// parseLogLine parses log by row.
func parseLogLine(podConfig *runtimeapi.PodSandboxConfig, logPath string) []logMessage {
	path := filepath.Join(podConfig.LogDirectory, logPath)
	f, err := os.Open(path)
	framework.ExpectNoError(err, "failed to open log file: %v", err)
	framework.Logf("Open log file %s", path)
	defer f.Close()

	var msg logMessage
	var msgLog []logMessage

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// to determine whether the log is Docker format or CRI format.
		if strings.HasPrefix(line, "{") {
			parseDockerJSONLog([]byte(line), &msg)
		} else {
			parseCRILog(line, &msg)
		}

		msgLog = append(msgLog, msg)
	}

	if err := scanner.Err(); err != nil {
		framework.ExpectNoError(err, "failed to read log by row: %v", err)
	}
	framework.Logf("Parse container log succeed")

	return msgLog
}

// verifyLogContents verifies the contents of container log.
func verifyLogContents(podConfig *runtimeapi.PodSandboxConfig, logPath string, log string, stream streamType) {
	By("verify log contents")
	msgs := parseLogLine(podConfig, logPath)

	found := false
	for _, msg := range msgs {
		if msg.log == log && msg.stream == stream {
			found = true
			break
		}
	}
	Expect(found).To(BeTrue(), "expected log %q (stream=%q) not found in logs %+v", log, stream, msgs)
}
