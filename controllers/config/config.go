package config

import (
	"errors"
	"path/filepath"
	"time"

	"code.cloudfoundry.org/korifi/tools"
)

const (
	DefaultExternalProtocol = "https"
)

type ControllerConfig struct {
	// components
	IncludeKpackImageBuilder bool `yaml:"includeKpackImageBuilder"`
	IncludeJobTaskRunner     bool `yaml:"includeJobTaskRunner"`
	IncludeStatefulsetRunner bool `yaml:"includeStatefulsetRunner"`
	IncludeContourRouter     bool `yaml:"includeContourRouter"`

	// core controllers
	ApiServerURL                string            `yaml:"apiServerUrl"`
	CFProcessDefaults           CFProcessDefaults `yaml:"cfProcessDefaults"`
	CFRootNamespace             string            `yaml:"cfRootNamespace"`
	ContainerRegistrySecretName string            `yaml:"containerRegistrySecretName"`
	TaskTTL                     string            `yaml:"taskTTL"`
	WorkloadsTLSSecretName      string            `yaml:"workloads_tls_secret_name"`
	WorkloadsTLSSecretNamespace string            `yaml:"workloads_tls_secret_namespace"`
	BuilderName                 string            `yaml:"builderName"`
	RunnerName                  string            `yaml:"runnerName"`
	NamespaceLabels             map[string]string `yaml:"namespaceLabels"`

	// job-task-runner
	JobTTL string `yaml:"jobTTL"`

	// kpack-image-builder
	ClusterBuilderName        string `yaml:"clusterBuilderName"`
	BuilderServiceAccount     string `yaml:"builderServiceAccount"`
	ContainerRepositoryPrefix string `yaml:"containerRepositoryPrefix"`
	ContainerRegistryType     string `yaml:"containerRegistryType"`
}

type CFProcessDefaults struct {
	MemoryMB    int64  `yaml:"memoryMB"`
	DiskQuotaMB int64  `yaml:"diskQuotaMB"`
	Timeout     *int64 `yaml:"timeout"`
}

const (
	defaultTaskTTL       = 30 * 24 * time.Hour
	defaultTimeout int64 = 60
	defaultJobTTL        = 24 * time.Hour
)

func LoadFromPath(path string) (*ControllerConfig, error) {
	var config ControllerConfig
	err := tools.LoadConfigInto(&config, path)

	err = config.validate()

	if err != nil {
		return nil, err
	}

	if config.CFProcessDefaults.Timeout == nil {
		config.CFProcessDefaults.Timeout = tools.PtrTo(defaultTimeout)
	}

	return &config, nil
}

func (c *ControllerConfig) validate() error {
	if c.ApiServerURL == "" {
		return errors.New("ApiServerURL not specified")
	}

	// TODO: Sanitize the URL
	c.ApiServerURL = DefaultExternalProtocol + "://" + c.ApiServerURL

	return nil
}

func (c *ControllerConfig) WorkloadsTLSSecretNameWithNamespace() string {
	if c.WorkloadsTLSSecretName == "" {
		return ""
	}
	return filepath.Join(c.WorkloadsTLSSecretNamespace, c.WorkloadsTLSSecretName)
}

func (c *ControllerConfig) ParseTaskTTL() (time.Duration, error) {
	if c.TaskTTL == "" {
		return defaultTaskTTL, nil
	}

	return tools.ParseDuration(c.TaskTTL)
}

func (c *ControllerConfig) ParseJobTTL() (time.Duration, error) {
	if c.JobTTL == "" {
		return defaultJobTTL, nil
	}

	return tools.ParseDuration(c.JobTTL)
}
