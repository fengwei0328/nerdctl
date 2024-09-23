/*
   Copyright The containerd Authors.

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

package nerdtest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/containerd/nerdctl/v2/pkg/buildkitutil"
	"github.com/containerd/nerdctl/v2/pkg/inspecttypes/dockercompat"
	"github.com/containerd/nerdctl/v2/pkg/inspecttypes/native"
	"github.com/containerd/nerdctl/v2/pkg/rootlessutil"
	"github.com/containerd/nerdctl/v2/pkg/testutil"
	"github.com/containerd/nerdctl/v2/pkg/testutil/test"
)

func Setup() {
	test.CustomCommand(nerdctlSetup)
}

// Nerdctl specific config key and values
var NerdctlToml test.ConfigKey = "NerdctlToml"
var HostsDir test.ConfigKey = "HostsDir"
var DataRoot test.ConfigKey = "DataRoot"
var Namespace test.ConfigKey = "Namespace"

var Mode test.ConfigKey = "Mode"
var ModePrivate test.ConfigValue = "Private"
var IPv6 test.ConfigKey = "IPv6Test"
var Only test.ConfigValue = "Only"

var OnlyIPv6 = test.MakeRequirement(func(data test.Data) (ret bool, mess string) {
	ret = testutil.GetEnableIPv6()
	if !ret {
		mess = "runner skips IPv6 compatible tests in the non-IPv6 environment"
	}
	data.WithConfig(IPv6, Only)
	return ret, mess
})

var Private = test.MakeRequirement(func(data test.Data) (ret bool, mess string) {
	data.WithConfig(Mode, ModePrivate)
	return true, ""
})

var Docker = test.MakeRequirement(func(data test.Data) (ret bool, mess string) {
	ret = testutil.GetTarget() == testutil.Docker
	if ret {
		mess = "current target is docker"
	} else {
		mess = "current target is not docker"
	}
	return ret, mess
})

var Rootless = test.MakeRequirement(func(data test.Data) (ret bool, mess string) {
	ret = rootlessutil.IsRootless()
	if ret {
		mess = "environment is rootless"
	} else {
		mess = "environment is rootful"
	}
	return ret, mess
})

var Build = test.MakeRequirement(func(data test.Data) (ret bool, mess string) {
	// FIXME: shouldn't we run buildkitd in a container? At least for testing, that would be so much easier than
	// against the host install
	ret = true
	mess = ""
	if testutil.GetTarget() == testutil.Nerdctl {
		_, err := buildkitutil.GetBuildkitHost(testutil.Namespace)
		if err != nil {
			ret = false
			mess = fmt.Sprintf("test requires buildkitd: %+v", err)
		}
	}
	return ret, mess
})

type NerdCommand struct {
	test.GenericCommand
	// FIXME: annoying - forces custom Clone, etc
	Target string
}

// Run does override the generic command run, as we are testing both docker and nerdctl
func (nc *NerdCommand) Run(expect *test.Expected) {
	// We are not in the business of testing docker error output, so, spay expect for errors testing, if any
	if expect != nil && nc.Target != testutil.Nerdctl {
		expect.Errors = nil
	}

	nc.GenericCommand.Run(expect)
}

// Clone is overridden as well, as we need to pass along the target
func (nc *NerdCommand) Clone() test.Command {
	return &NerdCommand{
		GenericCommand: *((nc.GenericCommand.Clone()).(*test.GenericCommand)),
		Target:         nc.Target,
	}
}

// InspectContainer is a helper that can be used inside custom commands or Setup
func InspectContainer(helpers test.Helpers, name string) dockercompat.Container {
	var dc []dockercompat.Container
	cmd := helpers.Command("container", "inspect", name)
	cmd.Run(&test.Expected{
		ExitCode: 0,
		Output: func(stdout string, info string, t *testing.T) {
			err := json.Unmarshal([]byte(stdout), &dc)
			assert.NilError(t, err, "Unable to unmarshal output\n"+info)
			assert.Equal(t, 1, len(dc), "Unexpectedly got multiple results\n"+info)
		},
	})
	return dc[0]
}

func InspectVolume(helpers test.Helpers, name string, args ...string) native.Volume {
	var dc []native.Volume
	cmdArgs := append([]string{"volume", "inspect"}, args...)
	cmdArgs = append(cmdArgs, name)

	cmd := helpers.Command(cmdArgs...)
	cmd.Run(&test.Expected{
		ExitCode: 0,
		Output: func(stdout string, info string, t *testing.T) {
			err := json.Unmarshal([]byte(stdout), &dc)
			assert.NilError(t, err, "Unable to unmarshal output\n"+info)
			assert.Equal(t, 1, len(dc), "Unexpectedly got multiple results\n"+info)
		},
	})
	return dc[0]
}

func InspectNetwork(helpers test.Helpers, name string, args ...string) dockercompat.Network {
	var dc []dockercompat.Network
	cmdArgs := append([]string{"network", "inspect"}, args...)
	cmdArgs = append(cmdArgs, name)

	cmd := helpers.Command(cmdArgs...)
	cmd.Run(&test.Expected{
		ExitCode: 0,
		Output: func(stdout string, info string, t *testing.T) {
			err := json.Unmarshal([]byte(stdout), &dc)
			assert.NilError(t, err, "Unable to unmarshal output\n"+info)
			assert.Equal(t, 1, len(dc), "Unexpectedly got multiple results\n"+info)
		},
	})
	return dc[0]
}

func nerdctlSetup(testCase *test.Case, t *testing.T) test.Command {
	t.Helper()

	var testUtilBase *testutil.Base
	dt := testCase.Data
	var pvNamespace string
	inherited := false

	if dt.ReadConfig(IPv6) != Only && testutil.GetEnableIPv6() {
		t.Skip("runner skips non-IPv6 compatible tests in the IPv6 environment")
	}

	if dt.ReadConfig(Mode) == ModePrivate {
		// If private was inherited, we already got a configured namespace
		if dt.ReadConfig(Namespace) != "" {
			pvNamespace = string(dt.ReadConfig(Namespace))
			inherited = true
		} else {
			// Otherwise, we need to set everything up
			pvNamespace = testCase.Data.Identifier()
			dt.WithConfig(Namespace, test.ConfigValue(pvNamespace))
			testCase.Env["DOCKER_CONFIG"] = testCase.Data.TempDir()
			testCase.Env["NERDCTL_TOML"] = filepath.Join(testCase.Data.TempDir(), "nerdctl.toml")
			dt.WithConfig(HostsDir, test.ConfigValue(testCase.Data.TempDir()))
			dt.WithConfig(DataRoot, test.ConfigValue(testCase.Data.TempDir()))
		}
		testUtilBase = testutil.NewBaseWithNamespace(t, pvNamespace)
		if testUtilBase.Target == testutil.Docker {
			// For docker, just disable parallel
			testCase.NoParallel = true
		}
	} else if dt.ReadConfig(Namespace) != "" {
		pvNamespace = string(dt.ReadConfig(Namespace))
		testUtilBase = testutil.NewBaseWithNamespace(t, pvNamespace)
	} else {
		testUtilBase = testutil.NewBase(t)
	}

	// If we were passed custom content for NerdctlToml, save it
	// Not happening if this is not nerdctl of course
	if testUtilBase.Target == testutil.Nerdctl && dt.ReadConfig(NerdctlToml) != "" {
		dest := filepath.Join(testCase.Data.TempDir(), "nerdctl.toml")
		testCase.Env["NERDCTL_TOML"] = dest
		err := os.WriteFile(dest, []byte(dt.ReadConfig(NerdctlToml)), 0400)
		assert.NilError(t, err, "failed to write custom nerdctl toml file for test")
	}

	// Build the base
	baseCommand := &NerdCommand{}
	baseCommand.WithBinary(testUtilBase.Binary)
	baseCommand.WithArgs(testUtilBase.Args...)
	baseCommand.WithEnv(testCase.Env)
	baseCommand.WithT(t)
	baseCommand.WithTempDir(testCase.Data.TempDir())
	baseCommand.Target = testUtilBase.Target

	if testUtilBase.Target == testutil.Nerdctl {
		if dt.ReadConfig(HostsDir) != "" {
			baseCommand.GenericCommand.WithArgs("--hosts-dir=" + string(dt.ReadConfig(HostsDir)))
		}

		if dt.ReadConfig(DataRoot) != "" {
			baseCommand.GenericCommand.WithArgs("--data-root=" + string(dt.ReadConfig(DataRoot)))
		}
	}

	// If we were in a custom namespace, not inherited - make sure we clean up the namespace
	// FIXME: this is broken, and custom namespaces are not cleaned properly
	if testUtilBase.Target == testutil.Nerdctl && pvNamespace != "" && !inherited {
		cleanup := func() {
			cl := baseCommand.Clone()
			cl.WithArgs("namespace", "remove", pvNamespace)
			cl.Run(nil)
		}
		cleanup()
		t.Cleanup(cleanup)
	}

	// Attach the base command
	return baseCommand
}
