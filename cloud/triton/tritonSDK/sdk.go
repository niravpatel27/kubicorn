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

package tritonSDK

import (
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"os"

	triton "github.com/joyent/triton-go"
	"github.com/joyent/triton-go/authentication"
	"github.com/joyent/triton-go/compute"
	"github.com/joyent/triton-go/network"
)

type Sdk struct {
	Compute *compute.ComputeClient
	Network *network.NetworkClient
}

type ServicePrincipal struct {
	KeyID       string
	AccountName string
	UserName    string
	KeyMaterial string
	RegionURL   string
}

func NewSdk() (*Sdk, error) {

	keyID := "f7:75:b3:53:fe:d5:5d:26:13:2a:7f:9b:6b:2e:94:94"
	if keyID == "" {
		return nil, fmt.Errorf("Empty $TRITON_KEY_ID")
	}
	accountName := "niravpatel27"
	if keyID == "" {
		return nil, fmt.Errorf("Empty $TRITON_KEY_ID")
	}
	keyMaterial := os.Getenv("TRITON_KEY_MATERIAL")
	userName := os.Getenv("TRITON_USER")

	var signer authentication.Signer
	var err error
	if keyMaterial == "" {
		input := authentication.SSHAgentSignerInput{
			KeyID:       keyID,
			AccountName: accountName,
		}
		signer, err = authentication.NewSSHAgentSigner(input)
		if err != nil {
			log.Fatalf("Error Creating SSH Agent Signer: %v", err)
		}
	} else {
		var keyBytes []byte
		if _, err = os.Stat(keyMaterial); err == nil {
			keyBytes, err = ioutil.ReadFile(keyMaterial)
			if err != nil {
				log.Fatalf("Error reading key material from %s: %s",
					keyMaterial, err)
			}
			block, _ := pem.Decode(keyBytes)
			if block == nil {
				log.Fatalf(
					"Failed to read key material '%s': no key found", keyMaterial)
			}

			if block.Headers["Proc-Type"] == "4,ENCRYPTED" {
				log.Fatalf(
					"Failed to read key '%s': password protected keys are\n"+
						"not currently supported. Please decrypt the key prior to use.", keyMaterial)
			}

		} else {
			keyBytes = []byte(keyMaterial)
		}

		input := authentication.PrivateKeySignerInput{
			KeyID:              keyID,
			PrivateKeyMaterial: keyBytes,
			AccountName:        accountName,
			Username:           userName,
		}
		signer, err = authentication.NewPrivateKeySigner(input)
		if err != nil {
			log.Fatalf("Error Creating SSH Private Key Signer: %v", err)
		}
	}

	config := &triton.ClientConfig{
		TritonURL:   "https://us-east-1.api.joyent.com",
		AccountName: accountName,
		Username:    userName,
		Signers:     []authentication.Signer{signer},
	}

	computeClient, err := compute.NewClient(config)
	if err != nil {
		log.Fatalf("Compute NewClient(): %v", err)
	}
	networkClient, err := network.NewClient(config)
	if err != nil {
		log.Fatalf("Network NewClient(): %v", err)
	}
	sdk := &Sdk{Compute: computeClient, Network: networkClient}
	return sdk, nil
}
