// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controlplane

import (
	"context"
	"fmt"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	"github.com/gardener/gardener/extensions/pkg/webhook/controlplane/genericmutator"

	"github.com/Masterminds/semver"
	"github.com/coreos/go-systemd/unit"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	kubeletconfigv1beta1 "k8s.io/kubelet/config/v1beta1"
)

// NewEnsurer creates a new controlplane ensurer.
func NewEnsurer(logger logr.Logger) genericmutator.Ensurer {
	return &ensurer{
		logger: logger.WithName("alicloud-controlplane-ensurer"),
	}
}

type ensurer struct {
	genericmutator.NoopEnsurer
	logger logr.Logger
}

// EnsureKubeAPIServerDeployment ensures that the kube-apiserver deployment conforms to the provider requirements.
func (e *ensurer) EnsureKubeAPIServerDeployment(ctx context.Context, ectx genericmutator.EnsurerContext, new, old *appsv1.Deployment) error {
	ps := &new.Spec.Template.Spec
	if c := extensionswebhook.ContainerWithName(ps.Containers, "kube-apiserver"); c != nil {
		cluster, err := ectx.GetCluster(ctx)
		if err != nil {
			return err
		}
		ver, err := semver.NewVersion(cluster.Shoot.Spec.Kubernetes.Version)
		if err != nil {
			return fmt.Errorf("cannot parse shoot k8s cluster version: %v", err)
		}
		ensureKubeAPIServerCommandLineArgs(c, ver)
	}
	return nil
}

// EnsureKubeControllerManagerDeployment ensures that the kube-controller-manager deployment conforms to the provider requirements.
func (e *ensurer) EnsureKubeControllerManagerDeployment(ctx context.Context, ectx genericmutator.EnsurerContext, new, old *appsv1.Deployment) error {
	ps := &new.Spec.Template.Spec
	if c := extensionswebhook.ContainerWithName(ps.Containers, "kube-controller-manager"); c != nil {
		ensureKubeControllerManagerCommandLineArgs(c)
	}
	return nil
}

func ensureKubeAPIServerCommandLineArgs(c *corev1.Container, ver *semver.Version) {
	// Ensure CSI-related admission plugins
	c.Command = extensionswebhook.EnsureNoStringWithPrefixContains(c.Command, "--enable-admission-plugins=",
		"PersistentVolumeLabel", ",")
	c.Command = extensionswebhook.EnsureStringWithPrefixContains(c.Command, "--disable-admission-plugins=",
		"PersistentVolumeLabel", ",")

	// Ensure CSI-related feature gates
	c.Command = extensionswebhook.EnsureNoStringWithPrefixContains(c.Command, "--feature-gates=",
		"ExpandInUsePersistentVolumes=false", ",")
	c.Command = extensionswebhook.EnsureNoStringWithPrefixContains(c.Command, "--feature-gates=",
		"ExpandCSIVolumes=false", ",")
	c.Command = extensionswebhook.EnsureNoStringWithPrefixContains(c.Command, "--feature-gates=",
		"CSINodeInfo=false", ",")
	c.Command = extensionswebhook.EnsureNoStringWithPrefixContains(c.Command, "--feature-gates=",
		"CSIDriverRegistry=false", ",")

	kVersion14 := semver.MustParse("v1.14")
	kVersion16 := semver.MustParse("v1.16")

	if ver.LessThan(kVersion16) {
		if ver.LessThan(kVersion14) {
			c.Command = extensionswebhook.EnsureStringWithPrefixContains(c.Command, "--feature-gates=",
				"CSINodeInfo=true", ",")
			c.Command = extensionswebhook.EnsureStringWithPrefixContains(c.Command, "--feature-gates=",
				"CSIDriverRegistry=true", ",")
		} else {
			c.Command = extensionswebhook.EnsureStringWithPrefixContains(c.Command, "--feature-gates=",
				"ExpandCSIVolumes=true", ",")
			c.Command = extensionswebhook.EnsureStringWithPrefixContains(c.Command, "--feature-gates=",
				"ExpandInUsePersistentVolumes=true", ",")
		}
	}
}

func ensureKubeControllerManagerCommandLineArgs(c *corev1.Container) {
	c.Command = extensionswebhook.EnsureStringWithPrefix(c.Command, "--cloud-provider=", "external")
}

// EnsureKubeletServiceUnitOptions ensures that the kubelet.service unit options conform to the provider requirements.
func (e *ensurer) EnsureKubeletServiceUnitOptions(ctx context.Context, ectx genericmutator.EnsurerContext, new, old []*unit.UnitOption) ([]*unit.UnitOption, error) {
	if opt := extensionswebhook.UnitOptionWithSectionAndName(new, "Service", "ExecStart"); opt != nil {
		command := extensionswebhook.DeserializeCommandLine(opt.Value)
		command = ensureKubeletCommandLineArgs(command)
		opt.Value = extensionswebhook.SerializeCommandLine(command, 1, " \\\n    ")
	}
	return new, nil
}

func ensureKubeletCommandLineArgs(command []string) []string {
	command = extensionswebhook.EnsureStringWithPrefix(command, "--provider-id=", "${PROVIDER_ID}")
	command = extensionswebhook.EnsureStringWithPrefix(command, "--cloud-provider=", "external")
	command = extensionswebhook.EnsureStringWithPrefix(command, "--enable-controller-attach-detach=", "true")
	return command
}

// EnsureKubeletConfiguration ensures that the kubelet configuration conforms to the provider requirements.
func (e *ensurer) EnsureKubeletConfiguration(ctx context.Context, ectx genericmutator.EnsurerContext, new, old *kubeletconfigv1beta1.KubeletConfiguration) error {
	// Ensure CSI-related feature gates
	if new.FeatureGates == nil {
		new.FeatureGates = make(map[string]bool)
	}
	cluster, err := ectx.GetCluster(ctx)
	if err != nil {
		return err
	}
	ver, err := semver.NewVersion(cluster.Shoot.Spec.Kubernetes.Version)
	if err != nil {
		return fmt.Errorf("Can not parse shoot k8s cluster version: %v", err)
	}

	kVersion14 := semver.MustParse("v1.14")
	kVersion16 := semver.MustParse("v1.16")

	if ver.LessThan(kVersion16) {
		if ver.LessThan(kVersion14) {
			new.FeatureGates["CSINodeInfo"] = true
			new.FeatureGates["CSIDriverRegistry"] = true
		} else {
			new.FeatureGates["ExpandCSIVolumes"] = true
		}
	}

	return nil
}
