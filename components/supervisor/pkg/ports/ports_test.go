// Copyright (c) 2020 TypeFox GmbH. All rights reserved.
// Licensed under the GNU Affero General Public License (AGPL).
// See License-AGPL.txt in the project root for license information.

package ports

import (
	"context"
	"io"
	"io/ioutil"
	"sync"
	"testing"

	"github.com/gitpod-io/gitpod/supervisor/api"
	"github.com/gitpod-io/gitpod/supervisor/pkg/gitpod"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

func TestPortsUpdateState(t *testing.T) {
	type ExposureExpectation []ExposedPort
	type UpdateExpectation []*Diff
	type ConfigChange struct {
		workspace []*gitpod.PortConfig
		instance  []*gitpod.PortsItems
	}
	type Change struct {
		Config     *ConfigChange
		Served     []ServedPort
		Exposed    []ExposedPort
		ConfigErr  error
		ServedErr  error
		ExposedErr error
	}
	tests := []struct {
		Desc             string
		InternalPorts    []uint32
		Changes          []Change
		ExpectedExposure ExposureExpectation
		ExpectedUpdates  UpdateExpectation
	}{
		{
			Desc: "basic locally served",
			Changes: []Change{
				{Served: []ServedPort{{8080, true}}},
				{Exposed: []ExposedPort{{LocalPort: 8080, GlobalPort: 60000}}},
				{Served: []ServedPort{{8080, true}, {60000, false}}},
				{Served: []ServedPort{{60000, false}}},
				{Served: []ServedPort{}},
			},
			ExpectedExposure: []ExposedPort{
				{LocalPort: 8080, GlobalPort: 60000},
			},
			ExpectedUpdates: UpdateExpectation{
				{Added: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 60000, Served: true}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 60000, Served: true, Exposed: &api.PortsStatus_ExposedPortInfo{OnExposed: api.OnPortExposedAction_notify_private, Visibility: api.PortVisibility_private}}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 60000, Served: false, Exposed: &api.PortsStatus_ExposedPortInfo{OnExposed: api.OnPortExposedAction_notify_private, Visibility: api.PortVisibility_private}}}},
			},
		},
		{
			Desc: "basic globally served",
			Changes: []Change{
				{Served: []ServedPort{{8080, false}}},
				{Served: []ServedPort{}},
			},
			ExpectedExposure: []ExposedPort{
				{LocalPort: 8080, GlobalPort: 8080},
			},
			ExpectedUpdates: UpdateExpectation{
				{Added: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 8080, Served: true}}},
				{Removed: []uint32{8080}},
			},
		},
		{
			Desc: "basic port publically exposed",
			Changes: []Change{
				{Exposed: []ExposedPort{{LocalPort: 8080, GlobalPort: 8080, Public: false, URL: "foobar"}}},
				{Exposed: []ExposedPort{{LocalPort: 8080, GlobalPort: 8080, Public: true, URL: "foobar"}}},
				{Served: []ServedPort{{Port: 8080}}},
			},
			ExpectedUpdates: UpdateExpectation{
				{Added: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 8080, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_private, Url: "foobar", OnExposed: api.OnPortExposedAction_notify_private}}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 8080, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, Url: "foobar", OnExposed: api.OnPortExposedAction_notify_private}}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 8080, Served: true, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, Url: "foobar", OnExposed: api.OnPortExposedAction_notify_private}}}},
			},
		},
		{
			Desc:          "internal ports served",
			InternalPorts: []uint32{8080},
			Changes: []Change{
				{Served: []ServedPort{}},
				{Served: []ServedPort{{8080, false}}},
			},

			ExpectedExposure: ExposureExpectation(nil),
			ExpectedUpdates:  UpdateExpectation(nil),
		},
		{
			Desc: "serving configured workspace port",
			Changes: []Change{
				{Config: &ConfigChange{
					workspace: []*gitpod.PortConfig{
						{Port: 8080, OnOpen: "open-browser"},
						{Port: 9229, OnOpen: "ignore", Visibility: "private"},
					},
				}},
				{
					Exposed: []ExposedPort{
						{LocalPort: 8080, GlobalPort: 8080, Public: true, URL: "8080-foobar"},
						{LocalPort: 9229, GlobalPort: 9229, Public: false, URL: "9229-foobar"},
					},
				},
				{
					Served: []ServedPort{
						{8080, false},
						{9229, true},
					},
				},
			},
			ExpectedExposure: []ExposedPort{
				{LocalPort: 8080, Public: true},
				{LocalPort: 9229},
				{LocalPort: 9229, GlobalPort: 60000},
			},
			ExpectedUpdates: UpdateExpectation{
				{Added: []*api.PortsStatus{{LocalPort: 8080}, {LocalPort: 9229}}},
				{Updated: []*api.PortsStatus{
					{LocalPort: 8080, GlobalPort: 8080, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, Url: "8080-foobar", OnExposed: api.OnPortExposedAction_open_browser}},
					{LocalPort: 9229, GlobalPort: 9229, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_private, Url: "9229-foobar", OnExposed: api.OnPortExposedAction_ignore}},
				}},
				{Updated: []*api.PortsStatus{
					{LocalPort: 8080, GlobalPort: 8080, Served: true, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, Url: "8080-foobar", OnExposed: api.OnPortExposedAction_open_browser}},
					{LocalPort: 9229, GlobalPort: 60000, Served: true, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_private, Url: "9229-foobar", OnExposed: api.OnPortExposedAction_ignore}},
				}},
			},
		},
		{
			Desc: "serving port from the configured port range",
			Changes: []Change{
				{Config: &ConfigChange{
					instance: []*gitpod.PortsItems{{
						OnOpen: "open-browser",
						Port:   "4000-5000",
					}},
				}},
				{Served: []ServedPort{{4040, true}}},
				{Exposed: []ExposedPort{{LocalPort: 4040, GlobalPort: 60000, Public: true, URL: "4040-foobar"}}},
				{Served: []ServedPort{{4040, true}, {60000, false}}},
			},
			ExpectedExposure: []ExposedPort{
				{LocalPort: 4040, GlobalPort: 60000, Public: true},
			},
			ExpectedUpdates: UpdateExpectation{
				{Added: []*api.PortsStatus{{LocalPort: 4040, GlobalPort: 60000, Served: true}}},
				{Updated: []*api.PortsStatus{
					{LocalPort: 4040, GlobalPort: 60000, Served: true, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, Url: "4040-foobar", OnExposed: api.OnPortExposedAction_open_browser}},
				}},
			},
		},
		{
			Desc: "auto expose configured ports",
			Changes: []Change{
				{
					Config: &ConfigChange{workspace: []*gitpod.PortConfig{
						{Port: 8080, Visibility: "private"},
					}},
				},
				{
					Exposed: []ExposedPort{{LocalPort: 8080, GlobalPort: 8080, Public: false, URL: "foobar"}},
				},
				{
					Exposed: []ExposedPort{{LocalPort: 8080, GlobalPort: 8080, Public: true, URL: "foobar"}},
				},
				{
					Served: []ServedPort{{8080, true}},
				},
				{
					Exposed: []ExposedPort{{LocalPort: 8080, GlobalPort: 60000, Public: true, URL: "foobar"}},
				},
				{
					Served: []ServedPort{{8080, true}, {60000, false}},
				},
				{
					Served: []ServedPort{{60000, false}},
				},
				{
					Served: []ServedPort{},
				},
				{
					Served: []ServedPort{{8080, false}},
				},
			},
			ExpectedExposure: []ExposedPort{
				{LocalPort: 8080, Public: false},
				{LocalPort: 8080, GlobalPort: 60000, Public: true},
				{LocalPort: 8080, GlobalPort: 8080, Public: true},
			},
			ExpectedUpdates: UpdateExpectation{
				{Added: []*api.PortsStatus{{LocalPort: 8080}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 8080, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_private, OnExposed: api.OnPortExposedAction_notify, Url: "foobar"}}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 8080, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, OnExposed: api.OnPortExposedAction_notify, Url: "foobar"}}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 60000, Served: true, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, OnExposed: api.OnPortExposedAction_notify, Url: "foobar"}}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 60000, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, OnExposed: api.OnPortExposedAction_notify, Url: "foobar"}}}},
				{Updated: []*api.PortsStatus{{LocalPort: 8080, GlobalPort: 8080, Served: true, Exposed: &api.PortsStatus_ExposedPortInfo{Visibility: api.PortVisibility_public, OnExposed: api.OnPortExposedAction_notify, Url: "foobar"}}}},
			},
		},
		{
			Desc: "starting multiple proxies for the same served event",
			Changes: []Change{
				{
					Served: []ServedPort{{8080, true}, {3000, true}},
				},
			},
			ExpectedExposure: []ExposedPort{
				{LocalPort: 8080, GlobalPort: 60000},
				{LocalPort: 3000, GlobalPort: 59999},
			},
			ExpectedUpdates: UpdateExpectation{
				{Added: []*api.PortsStatus{
					{LocalPort: 8080, GlobalPort: 60000, Served: true},
					{LocalPort: 3000, GlobalPort: 59999, Served: true},
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.Desc, func(t *testing.T) {
			var (
				exposed = &testExposedPorts{
					Changes: make(chan []ExposedPort),
					Error:   make(chan error),
				}
				served = &testServedPorts{
					Changes: make(chan []ServedPort),
					Error:   make(chan error),
				}
				config = &testConfigService{
					Changes: make(chan *Configs),
					Error:   make(chan error),
				}

				pm    = NewManager(exposed, served, config, test.InternalPorts...)
				updts []*Diff
			)
			pm.proxyStarter = func(localPort uint32, globalPort uint32) (io.Closer, error) {
				return ioutil.NopCloser(nil), nil
			}

			var wg sync.WaitGroup
			wg.Add(3)
			go func() {
				defer wg.Done()
				pm.Run()
			}()
			go func() {
				defer wg.Done()
				defer close(config.Error)
				defer close(config.Changes)
				defer close(served.Error)
				defer close(served.Changes)
				defer close(exposed.Error)
				defer close(exposed.Changes)

				for _, c := range test.Changes {
					if c.Config != nil {
						change := &Configs{}
						change.workspaceConfigs = parseWorkspaceConfigs(c.Config.workspace)
						portConfigs, rangeConfigs := parseInstanceConfigs(c.Config.instance)
						change.instancePortConfigs = portConfigs
						change.instanceRangeConfigs = rangeConfigs
						config.Changes <- change
					} else if c.ConfigErr != nil {
						config.Error <- c.ConfigErr
					} else if c.Served != nil {
						served.Changes <- c.Served
					} else if c.ServedErr != nil {
						served.Error <- c.ServedErr
					} else if c.Exposed != nil {
						exposed.Changes <- c.Exposed
					} else if c.ExposedErr != nil {
						exposed.Error <- c.ExposedErr
					}
				}
			}()
			go func() {
				defer wg.Done()

				sub := pm.Subscribe()
				defer sub.Close()

				for up := range sub.Updates() {
					updts = append(updts, up)
				}
			}()

			wg.Wait()

			sortExposed := cmpopts.SortSlices(func(x, y ExposedPort) bool { return x.LocalPort < y.LocalPort })
			if diff := cmp.Diff(test.ExpectedExposure, ExposureExpectation(exposed.Exposures), sortExposed); diff != "" {
				t.Errorf("unexpected exposures (-want +got):\n%s", diff)
			}

			sorPorts := cmpopts.SortSlices(func(x, y uint32) bool { return x < y })
			sortPortStatus := cmpopts.SortSlices(func(x, y *api.PortsStatus) bool { return x.LocalPort < y.LocalPort })
			if diff := cmp.Diff(test.ExpectedUpdates, UpdateExpectation(updts), sorPorts, sortPortStatus); diff != "" {
				t.Errorf("unexpected updates (-want +got):\n%s", diff)
			}
		})
	}
}

type testConfigService struct {
	Changes chan *Configs
	Error   chan error
}

func (tep *testConfigService) Observe(ctx context.Context) (<-chan *Configs, <-chan error) {
	return tep.Changes, tep.Error
}

type testExposedPorts struct {
	Changes chan []ExposedPort
	Error   chan error

	Exposures []ExposedPort
	mu        sync.Mutex
}

func (tep *testExposedPorts) Observe(ctx context.Context) (<-chan []ExposedPort, <-chan error) {
	return tep.Changes, tep.Error
}

func (tep *testExposedPorts) Expose(ctx context.Context, local, global uint32, public bool) error {
	tep.mu.Lock()
	defer tep.mu.Unlock()

	tep.Exposures = append(tep.Exposures, ExposedPort{
		GlobalPort: global,
		LocalPort:  local,
		Public:     public,
	})
	return nil
}

type testServedPorts struct {
	Changes chan []ServedPort
	Error   chan error
}

func (tps *testServedPorts) Observe(ctx context.Context) (<-chan []ServedPort, <-chan error) {
	return tps.Changes, tps.Error
}
