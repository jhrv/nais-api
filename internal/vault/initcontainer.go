package vault

import (
	"fmt"
	"github.com/hashicorp/go-multierror"
	"github.com/nais/naisd/api/app"
	"github.com/spf13/viper"
	k8score "k8s.io/api/core/v1"
	"strconv"
)

const (
	mountPath = "/var/run/secrets/nais.io/vault"
	//EnvVaultAddr is the environment name for looking up the address of the Vault server
	EnvVaultAddr = "NAISD_VAULT_ADDR" //
	//EnvInitContainerImage is the environment name for looking up the init container to use
	EnvInitContainerImage = "NAISD_VAULT_INIT_CONTAINER_IMAGE"
	//EnvVaultAuthPath is the environment name for looking up the path to vault kubernetes auth backend
	EnvVaultAuthPath = "NAISD_VAULT_AUTH_PATH"
	//EnvVaultKVPath is the environment name for looking up the path to Vault KV mount
	EnvVaultKVPath = "NAISD_VAULT_KV_PATH"
	//EnvVaultEnabled is the environment name for looking up the enable/disable feature flag
	EnvVaultEnabled = "NAISD_VAULT_ENABLED"
)

type config struct {
	vaultAddr          string
	initContainerImage string
	authPath           string
	kvPath             string
	sidecar            bool
}

func (c config) validate() (bool, error) {

	var result = &multierror.Error{}

	if len(c.vaultAddr) == 0 {
		multierror.Append(result, fmt.Errorf("vault address not found in environment. Missing %s", EnvVaultAddr))
	}

	if len(c.initContainerImage) == 0 {
		multierror.Append(result, fmt.Errorf("vault address not found in environment. Missing %s", EnvInitContainerImage))
	}

	if len(c.authPath) == 0 {
		multierror.Append(result, fmt.Errorf("auth path not found in environment. Missing %s", EnvVaultAuthPath))
	}

	if len(c.kvPath) == 0 {
		multierror.Append(result, fmt.Errorf("kv path not found in environment. Missing %s", EnvVaultKVPath))
	}

	return result.ErrorOrNil() == nil, result.ErrorOrNil()

}

func init() {
	viper.BindEnv(EnvVaultAddr, EnvVaultAddr)
	viper.BindEnv(EnvInitContainerImage, EnvInitContainerImage)
	viper.BindEnv(EnvVaultAuthPath, EnvVaultAuthPath)
	viper.BindEnv(EnvVaultKVPath, EnvVaultKVPath)

	//temp feature flag. Disable by default
	viper.BindEnv(EnvVaultEnabled, EnvVaultEnabled)
	viper.SetDefault(EnvVaultEnabled, false)

}

type initializer struct {
	spec   app.Spec
	config config
}

//Initializer adds init containers
type Initializer interface {
	AddVaultContainers(podSpec *k8score.PodSpec) k8score.PodSpec
}

//Enabled checks if this Initalizer is enabled
func Enabled() bool {
	return viper.GetBool(EnvVaultEnabled)
}

//NewInitializer creates a new Initializer. Err if required env variables are not set.
func NewInitializer(spec app.Spec, sidecar bool) (Initializer, error) {
	config := config{
		vaultAddr:          viper.GetString(EnvVaultAddr),
		initContainerImage: viper.GetString(EnvInitContainerImage),
		authPath:           viper.GetString(EnvVaultAuthPath),
		kvPath:             viper.GetString(EnvVaultKVPath),
		sidecar:            sidecar,
	}

	if ok, err := config.validate(); !ok {
		return nil, err
	}

	return initializer{
		spec:   spec,
		config: config,
	}, nil
}

//Add init container to pod spec.
func (c initializer) AddVaultContainers(podSpec *k8score.PodSpec) k8score.PodSpec {
	volume, mount := volumeAndMount()

	//Add shared volume to pod
	podSpec.Volumes = append(podSpec.Volumes, volume)

	//"Main" container in the pod gets the shared volume mounted.
	mutatedContainers := make([]k8score.Container, 0, len(podSpec.Containers))
	for _, containerCopy := range podSpec.Containers {
		if containerCopy.Name == c.spec.Application {
			containerCopy.VolumeMounts = append(containerCopy.VolumeMounts, mount)
		}
		mutatedContainers = append(mutatedContainers, containerCopy)
	}
	podSpec.Containers = mutatedContainers

	//Finally add init container which also gets the shared volume mounted.
	podSpec.InitContainers = append(podSpec.InitContainers, c.vaultContainer(mount, false))
	if c.config.sidecar {
		podSpec.Containers = append(podSpec.Containers, c.vaultContainer(mount, true))
	}

	return *podSpec
}

func volumeAndMount() (k8score.Volume, k8score.VolumeMount) {
	name := "vault-secrets"
	volume := k8score.Volume{
		Name: name,
		VolumeSource: k8score.VolumeSource{
			EmptyDir: &k8score.EmptyDirVolumeSource{
				Medium: k8score.StorageMediumMemory,
			},
		},
	}

	mount := k8score.VolumeMount{
		Name:      name,
		MountPath: mountPath,
	}

	return volume, mount
}

func (c initializer) kvPath() string {
	return c.config.kvPath + "/" + c.spec.Application + "/" + c.spec.Namespace
}

func (c initializer) vaultRole() string {
	return c.spec.Application
}

func (c initializer) vaultContainer(mount k8score.VolumeMount, sidecar bool) k8score.Container {
	var name = "vks-init"
	if sidecar {
		name = "vks-sidecar"
	}

	return k8score.Container{
		Name:         name,
		VolumeMounts: []k8score.VolumeMount{mount},
		Image:        c.config.initContainerImage,
		Env: []k8score.EnvVar{
			{
				Name:  "VKS_VAULT_ADDR",
				Value: c.config.vaultAddr,
			},
			{
				Name:  "VKS_AUTH_PATH",
				Value: c.config.authPath,
			},
			{
				Name:  "VKS_KV_PATH",
				Value: c.kvPath(),
			},
			{
				Name:  "VKS_VAULT_ROLE",
				Value: c.vaultRole(),
			},
			{
				Name:  "VKS_SECRET_DEST_PATH",
				Value: mountPath,
			},
			{
				Name:  "VKS_IS_SIDECAR",
				Value: strconv.FormatBool(sidecar),
			},
		},
	}
}
