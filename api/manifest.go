package api

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/hashicorp/go-multierror"
	"github.com/imdario/mergo"
	"github.com/nais/naisd/api/naisrequest"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"regexp"
)

type Probe struct {
	Path             string
	InitialDelay     int `yaml:"initialDelay"`
	PeriodSeconds    int `yaml:"periodSeconds"`
	FailureThreshold int `yaml:"failureThreshold"`
	Timeout          int `yaml:"timeout"`
}

type Healthcheck struct {
	Liveness  Probe
	Readiness Probe
}

type ResourceList struct {
	Cpu    string
	Memory string
}

type ResourceRequirements struct {
	Limits   ResourceList
	Requests ResourceList
}

type PrometheusConfig struct {
	Enabled bool
	Port    string
	Path    string
}

type IstioConfig struct {
	Enabled bool
}

type NaisManifest struct {
	Team            string
	Image           string
	Port            int
	Healthcheck     Healthcheck
	PreStopHookPath string         `yaml:"preStopHookPath"`
	Prometheus      PrometheusConfig
	Istio           IstioConfig
	Replicas        Replicas
	Ingress         Ingress
	Resources       ResourceRequirements
	FasitResources  FasitResources `yaml:"fasitResources"`
	LeaderElection  bool           `yaml:"leaderElection"`
	Redis           bool           `yaml:"redis"`
	Alerts          []PrometheusAlertRule
	Logformat       string
	Logtransform    string
}

type Ingress struct {
	Disabled bool
}

type Replicas struct {
	Min                    int
	Max                    int
	CpuThresholdPercentage int `yaml:"cpuThresholdPercentage"`
}

type FasitResources struct {
	Used    []UsedResource
	Exposed []ExposedResource
}

type UsedResource struct {
	Alias        string
	ResourceType string            `yaml:"resourceType"`
	PropertyMap  map[string]string `yaml:"propertyMap"`
}

type ExposedResource struct {
	Alias          string
	ResourceType   string `yaml:"resourceType"`
	Path           string
	Description    string
	WsdlGroupId    string `yaml:"wsdlGroupId"`
	WsdlArtifactId string `yaml:"wsdlArtifactId"`
	WsdlVersion    string `yaml:"wsdlVersion"`
	SecurityToken  string `yaml:"securityToken"`
	AllZones       bool   `yaml:"allZones"`
}

type ValidationErrors struct {
	Errors []ValidationError
}

type ValidationError struct {
	ErrorMessage string
	Fields       map[string]string
}

type Field struct {
	Name  string
	Value string
}

func GenerateManifest(deploymentRequest naisrequest.Deploy) (naisManifest NaisManifest, err error) {

	manifest, err := downloadManifest(deploymentRequest)

	if err != nil {
		glog.Errorf("could not download manifest", err)
		return NaisManifest{}, err
	}

	if err := AddDefaultManifestValues(&manifest, deploymentRequest.Application); err != nil {
		glog.Errorf("Could not merge manifest %s", err)
		return NaisManifest{}, err
	}

	validationErrors := ValidateManifest(manifest)
	if len(validationErrors.Errors) != 0 {
		glog.Error("Invalid manifest: ", validationErrors.Error())
		return NaisManifest{}, validationErrors
	}

	return manifest, nil
}

func downloadManifest(deploymentRequest naisrequest.Deploy) (naisManifest NaisManifest, err error) {
	var urls []string
	var errors error

	if len(deploymentRequest.ManifestUrl) > 0 {
		urls = []string{deploymentRequest.ManifestUrl}
	} else {
		glog.Info("No manifest url specified. Using defaults")
		urls = createManifestUrl(deploymentRequest.Application, deploymentRequest.Version)
	}

	for _, url := range urls {
		if manifest, err := fetchManifest(url); err != nil {
			errors = multierror.Append(errors, err)
		} else {
			return manifest, nil
		}

	}

	return NaisManifest{}, errors
}

func createManifestUrl(application, version string) []string {
	return []string{
		fmt.Sprintf("https://repo.adeo.no/repository/raw/nais/%s/%s/nais.yaml", application, version),
		fmt.Sprintf("http://nexus.adeo.no/nexus/service/local/repositories/m2internal/content/nais/%s/%s/nais.yaml", application, version),
		fmt.Sprintf("http://nexus.adeo.no/nexus/service/local/repositories/m2internal/content/nais/%s/%s/%s.yaml", application, version, application+"-"+version),
	}
}

func AddDefaultManifestValues(manifest *NaisManifest, application string) error {
	return mergo.Merge(manifest, GetDefaultManifest(application))
}
func fetchManifest(url string) (NaisManifest, error) {
	glog.Infof("Fetching manifest from URL %s\n", url)
	response, err := http.Get(url)
	if err != nil {
		glog.Errorf("Could not fetch %s", err)
		return NaisManifest{}, fmt.Errorf("HTTP GET failed for url: %s. %s", url, err.Error())
	}

	defer response.Body.Close()

	if response.StatusCode > 299 {
		glog.Errorf("got HTTP status code %d fetching manifest from URL: %s", response.StatusCode, url)
		return NaisManifest{}, fmt.Errorf("got HTTP status code %d fetching manifest from URL: %s", response.StatusCode, url)
	}

	if body, err := ioutil.ReadAll(response.Body); err != nil {
		return NaisManifest{}, err
	} else {
		var manifest NaisManifest
		if err := yaml.Unmarshal(body, &manifest); err != nil {
			glog.Errorf("Could not unmarshal yaml %s from URL: %s", err, url)
			return NaisManifest{}, fmt.Errorf("unable to unmarshal %s from URL: %s", err.Error(), url)
		}
		glog.Infof("Got manifest %s", manifest)
		return manifest, err
	}
}

func ValidateManifest(manifest NaisManifest) ValidationErrors {
	validations := []func(NaisManifest) *ValidationError{
		validateImage,
		validateReplicasMax,
		validateReplicasMin,
		validateMinIsSmallerThanMax,
		validateCpuThreshold,
		validateRequestMemoryNotation,
		validateLimitsMemoryNotation,
		validateResources,
		validateAlertRules,
	}

	var validationErrors ValidationErrors
	for _, valfunc := range validations {
		if valError := valfunc(manifest); valError != nil {
			validationErrors.Errors = append(validationErrors.Errors, *valError)
		}
	}

	return validationErrors
}

func validateResources(manifest NaisManifest) *ValidationError {
	for _, resource := range manifest.FasitResources.Exposed {
		if resource.ResourceType == "" || resource.Alias == "" {
			return &ValidationError{
				"Alias and ResourceType must be specified",
				map[string]string{"Alias": resource.Alias},
			}
		}
	}
	for _, resource := range manifest.FasitResources.Used {
		if resource.ResourceType == "" || resource.Alias == "" {
			return &ValidationError{
				"Alias and ResourceType must be specified",
				map[string]string{"Alias": resource.Alias},
			}
		}
	}
	return nil
}

func validateImage(manifest NaisManifest) *ValidationError {
	if strings.LastIndex(manifest.Image, ":") > strings.LastIndex(manifest.Image, "/") {
		return &ValidationError{
			"Image cannot contain tag",
			map[string]string{"Image": manifest.Image},
		}
	}
	return nil
}

func validateMemoryNotation(key string, value string) *ValidationError {
	matched, err := regexp.MatchString("\\d+([EPTGMK]i?)?$", value)
	if err != nil {
		glog.Errorf("error while trying to match %s with regex: %s", value, err)
		matched = false
	}

	if !matched {
		err := new(ValidationError)
		err.ErrorMessage = "Not a valid memory value. Are you using correct notation?"
		err.Fields = make(map[string]string)
		err.Fields[key] = value
		return err
	}

	return nil
}

func validateRequestMemoryNotation(manifest NaisManifest) *ValidationError {
	return validateMemoryNotation("Resources.Requests.Memory", manifest.Resources.Requests.Memory)
}

func validateLimitsMemoryNotation(manifest NaisManifest) *ValidationError {
	return validateMemoryNotation("Resources.Limits.Memory", manifest.Resources.Limits.Memory)
}

func validateCpuThreshold(manifest NaisManifest) *ValidationError {
	if manifest.Replicas.CpuThresholdPercentage < 10 || manifest.Replicas.CpuThresholdPercentage > 90 {
		err := new(ValidationError)
		err.ErrorMessage = "CpuThreshold must be between 10 and 90."
		err.Fields = make(map[string]string)
		err.Fields["Replicas.CpuThreshold"] = strconv.Itoa(manifest.Replicas.CpuThresholdPercentage)
		return err

	}
	return nil

}

func validateMinIsSmallerThanMax(manifest NaisManifest) *ValidationError {
	if manifest.Replicas.Min > manifest.Replicas.Max {
		validationError := new(ValidationError)
		validationError.ErrorMessage = "Replicas.Min is larger than Replicas.Max."
		validationError.Fields = make(map[string]string)
		validationError.Fields["Replicas.Max"] = strconv.Itoa(manifest.Replicas.Max)
		validationError.Fields["Replicas.Min"] = strconv.Itoa(manifest.Replicas.Min)
		return validationError
	}
	return nil

}
func validateReplicasMin(manifest NaisManifest) *ValidationError {
	if manifest.Replicas.Min == 0 {
		validationError := new(ValidationError)
		validationError.ErrorMessage = "Replicas.Min is not set"
		validationError.Fields = make(map[string]string)
		validationError.Fields["Replicas.Min"] = strconv.Itoa(manifest.Replicas.Min)
		return validationError

	}
	return nil

}

func validateReplicasMax(manifest NaisManifest) *ValidationError {
	if manifest.Replicas.Max == 0 {
		validationError := new(ValidationError)
		validationError.ErrorMessage = "Replicas.Max is not set"
		validationError.Fields = make(map[string]string)
		validationError.Fields["Replicas.Max"] = strconv.Itoa(manifest.Replicas.Max)
		return validationError
	}
	return nil

}

func (errors ValidationErrors) Error() (s string) {
	for _, validationError := range errors.Errors {
		s += validationError.ErrorMessage + "\n"
		for k, v := range validationError.Fields {
			s += " - " + k + ": " + v + ".\n"
		}
	}
	return s
}
