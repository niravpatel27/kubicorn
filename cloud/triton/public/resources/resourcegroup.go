// Copyright © 2017 The Kubicorn Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/joyent/triton-go/compute"
	"github.com/joyent/triton-go/network"
	"github.com/kubicorn/kubicorn/apis/cluster"
	"github.com/kubicorn/kubicorn/cloud"
	"github.com/kubicorn/kubicorn/pkg/compare"
	"github.com/kubicorn/kubicorn/pkg/logger"
	"github.com/kubicorn/kubicorn/pkg/script"
)

const (
	MasterIPAttempts               = 100
	MasterIPSleepSecondsPerAttempt = 5
	PackageName                    = "k4-highcpu-kvm-1.75G"
	ImageName                      = "ubuntu-certified-16.04"
	ImageVersion                   = "20180222"
	NetworkName                    = "Joyent-SDC-Public"
	FabricNetwork                  = "My-Fabric-Network"
)

type ResourceGroup struct {
	Shared
	BootstrapScripts []string
	ServerPool       *cluster.ServerPool
}

// Actual returns the actual resource group in Triton if it exists.
func (r *ResourceGroup) Actual(immutable *cluster.Cluster) (*cluster.Cluster, cloud.Resource, error) {
	logger.Debug("resourcegroup.Actual")
	j, _ := json.Marshal(r)
	logger.Debug("Resource: %v", string(j))

	newResource := &ResourceGroup{
		Shared: Shared{
			Name: r.Name,
			Tags: map[string]string{
				"Name": r.ServerPool.Name,
			},
			Identifier: r.Shared.Identifier,
		},
	}

	if newResource.Identifier != "" {
		instance, err := Sdk.Compute.Instances().Get(context.Background(), &compute.GetInstanceInput{
			ID: newResource.Identifier,
		})
		newResource.Name = instance.Name
		newResource.Identifier = instance.ID

		if err != nil {
			return nil, nil, err
		}
	}
	return immutable, newResource, nil
}

// Expected will return the expected resource group as it would be defined in Triton
func (r *ResourceGroup) Expected(immutable *cluster.Cluster) (*cluster.Cluster, cloud.Resource, error) {
	logger.Debug("resourcegroup.Expected")
	newResource := &ResourceGroup{
		Shared: Shared{
			Name: r.Name,
			Tags: map[string]string{
				"Name": r.ServerPool.Name,
			},
			Identifier: r.Shared.Identifier,
		},
		ServerPool: r.ServerPool,
	}
	return immutable, newResource, nil
}

func (r *ResourceGroup) Apply(actual, expected cloud.Resource, immutable *cluster.Cluster) (*cluster.Cluster, cloud.Resource, error) {
	logger.Debug("device.Apply")
	expectedResource := expected.(*ResourceGroup)
	actualResource := actual.(*ResourceGroup)
	isEqual, err := compare.IsEqual(actualResource, expectedResource)
	if err != nil {
		return nil, nil, err
	}
	if isEqual {
		logger.Debug("device.Apply already equal")
		return immutable, expectedResource, nil
	}

	// if we are a node, we need to get thekubernetes master IP
	if r.ServerPool.Type == cluster.ServerPoolTypeNode {
		masterIPs, err := getMasterIP(r.Shared.Identifier)
		if err != nil {
			return nil, nil, err
		}
		immutable.ProviderConfig().KubernetesAPI.Endpoint = masterIPs[0]
		providerConfig := immutable.ProviderConfig()
		providerConfig.Values.ItemMap["INJECTEDMASTER"] = fmt.Sprintf("%s:%s", masterIPs[0], immutable.ProviderConfig().KubernetesAPI.Port)
		immutable.SetProviderConfig(providerConfig)
	}
	providerConfig := immutable.ProviderConfig()
	providerConfig.Values.ItemMap["INJECTEDPORT"] = immutable.ProviderConfig().KubernetesAPI.Port
	immutable.SetProviderConfig(providerConfig)

	boostrapScript, err := script.BuildBootstrapScript(r.ServerPool.BootstrapScripts, immutable)
	if err != nil {
		return nil, nil, err
	}

	images, err := Sdk.Compute.Images().List(context.Background(), &compute.ListImagesInput{
		Name:    ImageName,
		Version: ImageVersion,
	})

	if err != nil {
		logger.Debug("compute.Images.List: %v", err)
	}

	var img compute.Image
	if len(images) > 0 {
		img = *images[0]
	} else {
		logger.Debug("Unable to find an Image")
	}

	var net1 *network.Network
	var net2 *network.Network
	nets, err := Sdk.Network.List(context.Background(), &network.ListInput{})
	if err != nil {
		logger.Debug("Network List(): %v", err)
	}
	for _, found := range nets {
		if found.Name == NetworkName {
			net1 = found
		}
		if found.Name == FabricNetwork {
			net2 = found
		}
	}

	createInput := &compute.CreateInstanceInput{
		Name:     r.ServerPool.Name,
		Package:  PackageName,
		Image:    img.ID,
		Networks: []string{net1.Id, net2.Id},
		Metadata: map[string]string{
			"user-script": string(boostrapScript),
		},
		Tags: map[string]string{
			"name": r.ServerPool.Name,
		},
		CNS: compute.InstanceCNS{
			Services: []string{r.ServerPool.Name},
		},
	}
	created, err := Sdk.Compute.Instances().Create(context.Background(), createInput)
	if err != nil {
		return nil, nil, err
	}

	// if these are masters, we are not done until we have the master IP
	if r.ServerPool.Type == cluster.ServerPoolTypeMaster {
		masterIPs, err := getMasterIP(created.ID)
		if err != nil {
			return nil, nil, err
		}

		if len(masterIPs) == 0 {
			return nil, nil, fmt.Errorf("Unable to find master IP addresses")
		} else {
			logger.Debug("device.Apply master IP addresses %s", masterIPs[0])
			immutable.ProviderConfig().KubernetesAPI.Endpoint = masterIPs[0]
		}

	}

	newResource := &ResourceGroup{
		Shared: Shared{
			Name:       r.ServerPool.Name,
			Identifier: created.ID,
		},
		ServerPool: expected.(*ResourceGroup).ServerPool,
	}
	newResource.ServerPool.Identifier = created.ID

	logger.Debug("device.Apply newResource %v", newResource)

	return immutable, newResource, nil

}
func (r *ResourceGroup) Delete(actual cloud.Resource, immutable *cluster.Cluster) (*cluster.Cluster, cloud.Resource, error) {
	logger.Debug("resourcegroup.Delete")
	deleteResource := actual.(*ResourceGroup)
	if deleteResource.Identifier == "" {
		return nil, nil, fmt.Errorf("Unable to delete VPC resource without ID [%s]", deleteResource.Name)
	}

	newResource := &ResourceGroup{
		Shared: Shared{
			Name: r.Name,
			Tags: r.Tags,
		},
	}

	newCluster := r.immutableRender(newResource, immutable)
	return newCluster, newResource, nil
}

func (r *ResourceGroup) immutableRender(newResource cloud.Resource, inaccurateCluster *cluster.Cluster) *cluster.Cluster {
	logger.Debug("resourcegroup.Render")
	resourceGroup := newResource.(*ResourceGroup)
	newCluster := inaccurateCluster
	providerConfig := &cluster.ControlPlaneProviderConfig{}
	providerConfig.GroupIdentifier = resourceGroup.Identifier
	newCluster.Name = resourceGroup.Name
	newCluster.SetProviderConfig(providerConfig)
	return newCluster
}

func getMasterIP(identifier string) ([]string, error) {
	ret := make([]string, 3, 3)
	logger.Debug("device.getMasterIP attempting to get master public IP")
	for i := 0; i < MasterIPAttempts; i++ {

		instance, err := Sdk.Compute.Instances().Get(context.Background(), &compute.GetInstanceInput{
			ID: identifier,
		})

		logger.Debug("device.getMasterIP attempt %d to get master IP address", i)
		if err != nil {
			logger.Debug("device.getMasterIP error retrieving devices: %v", err)
			return ret, err
		}
		// we have master devices
		if len(instance.IPs) > 0 {
			return instance.IPs, nil
		}
		time.Sleep(time.Duration(MasterIPSleepSecondsPerAttempt) * time.Second)
	}
	return ret, nil
}
