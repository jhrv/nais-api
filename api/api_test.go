package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/nais/naisd/api/naisrequest"
	"github.com/nais/naisd/pkg/event"
	"github.com/stretchr/testify/assert"
	"goji.io"
	"goji.io/pat"
	"gopkg.in/h2non/gock.v1"
	"gopkg.in/yaml.v2"
	"k8s.io/client-go/kubernetes/fake"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	fakeDeploymentHandler = func(event deployment.Event) {}
)

type FakeDeployStatusViewer struct {
	deployStatusToReturn DeployStatus
	viewToReturn         DeploymentStatusView
	errToReturn          error
}

func (d FakeDeployStatusViewer) DeploymentStatusView(namespace, deployName string) (DeployStatus, DeploymentStatusView, error) {
	return d.deployStatusToReturn, d.viewToReturn, d.errToReturn
}

func TestAnIncorrectPayloadGivesError(t *testing.T) {
	api := Api{}

	body := strings.NewReader("gibberish")

	req, err := http.NewRequest("POST", "/deploy", body)

	if err != nil {
		panic("could not create req")
	}

	rr := httptest.NewRecorder()
	handler := http.Handler(appHandler(api.deploy))

	handler.ServeHTTP(rr, req)

	assert.Equal(t, 400, rr.Code)
}

func TestDeployStatusHandler(t *testing.T) {
	req, _ := http.NewRequest("GET", "/deploystatus/default/deployName", strings.NewReader("whatever"))

	t.Run("Return 404 if deploy status is not found", func(t *testing.T) {
		mux := goji.NewMux()

		api := Api{DeploymentStatusViewer: FakeDeployStatusViewer{
			errToReturn: fmt.Errorf("not Found"),
		}}

		mux.Handle(pat.Get("/deploystatus/:namespace/:deployName"), appHandler(api.deploymentStatusHandler))

		rr := httptest.NewRecorder()

		mux.ServeHTTP(rr, req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Correct http code for a given deploy status", func(t *testing.T) {

		tests := []struct {
			deployStatus     DeployStatus
			expectedHttpCode int
		}{
			{
				Success,
				200,
			},
			{
				Failed,
				500,
			},
			{
				InProgress,
				202,
			},
		}

		for _, test := range tests {
			mux := goji.NewMux()

			api := Api{
				DeploymentStatusViewer: FakeDeployStatusViewer{
					deployStatusToReturn: test.deployStatus,
				},
			}
			mux.Handle(pat.Get("/deploystatus/:namespace/:deployName"), appHandler(api.deploymentStatusHandler))

			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)

			assert.Equal(t, test.expectedHttpCode, rr.Code)
		}
	})
}

func TestNoManifestGivesError(t *testing.T) {
	api := Api{}

	manifestUrl := "http://repo.com/app"
	depReq := naisrequest.Deploy{
		Application:      "appname",
		Version:          "",
		FasitEnvironment: "",
		ManifestUrl:      manifestUrl,
		Zone:             "zone",
		Namespace:        "default",
	}

	defer gock.Off()

	gock.New("http://repo.com").
		Get("/app").
		Reply(400).
		JSON(map[string]string{"foo": "bar"})

	jsn, _ := json.Marshal(depReq)

	body := strings.NewReader(string(jsn))

	req, err := http.NewRequest("POST", "/deploy", body)

	if err != nil {
		panic("could not create req")
	}
	rr := httptest.NewRecorder()
	handler := http.Handler(appHandler(api.deploy))

	handler.ServeHTTP(rr, req)

	assert.Equal(t, 500, rr.Code)
	assert.Contains(t, string(rr.Body.Bytes()), manifestUrl)
}

func TestValidDeploymentRequestAndManifestCreateResources(t *testing.T) {
	appName := "appname"
	namespace := "default"
	fasitEnvironment := "environmentName"
	image := "name/Container"
	version := "123"
	resourceAlias := "alias1"
	resourceType := "db"
	zone := "zone"

	clientset := fake.NewSimpleClientset()

	api := Api{clientset, "https://fasit.local", "nais.example.tk", "test-cluster", false, false, nil, fakeDeploymentHandler}

	depReq := naisrequest.Deploy{
		Application:      appName,
		Version:          version,
		FasitEnvironment: fasitEnvironment,
		ManifestUrl:      "http://repo.com/app",
		Zone:             "zone",
		Namespace:        namespace,
	}

	manifest := NaisManifest{
		Image: image,
		Port:  321,
		Team:  teamName,
		FasitResources: FasitResources{
			Used: []UsedResource{{resourceAlias, resourceType, nil}},
		},
	}
	response := "anything"
	data, _ := yaml.Marshal(manifest)
	appInstanceResponse, _ := yaml.Marshal(response)

	defer gock.Off()
	gock.New("https://fasit.local").
		Get("/api/v2/scopedresource").
		MatchParam("alias", NavTruststoreFasitAlias).
		Reply(200).File("testdata/fasitTruststoreResponse.json")

	gock.New("https://fasit.local").
		Get("/api/v2/resources/3024713/file/keystore").
		Reply(200).
		BodyString("")

	gock.New("http://repo.com").
		Get("/app").
		Reply(200).
		BodyString(string(data))

	gock.New("https://fasit.local").
		Get("/api/v2/scopedresource").
		MatchParam("alias", resourceAlias).
		MatchParam("type", resourceType).
		MatchParam("environment", fasitEnvironment).
		MatchParam("application", appName).
		MatchParam("zone", zone).
		Reply(200).File("testdata/fasitResponse.json")

	gock.New("https://fasit.local").
		Get(fmt.Sprintf("/api/v2/environments/%s", fasitEnvironment)).
		Reply(200).
		JSON(map[string]string{"environmentclass": "u"})

	gock.New("https://fasit.local").
		Get("/api/v2/applications/" + appName).
		Reply(200).
		BodyString("anything")

	gock.New("https://fasit.local").
		Post("/api/v2/applicationinstances/").
		Reply(200).
		BodyString(string(appInstanceResponse))

	jsn, _ := json.Marshal(depReq)

	body := strings.NewReader(string(jsn))

	req, _ := http.NewRequest("POST", "/deploy", body)

	rr := httptest.NewRecorder()
	handler := http.Handler(appHandler(api.deploy))

	handler.ServeHTTP(rr, req)

	assert.Equal(t, 200, rr.Code)
	assert.True(t, gock.IsDone())
	assert.Equal(t, "result: \n- created deployment\n- created secret\n- created service\n- created ingress\n- created autoscaler\n- created serviceaccount\n- created rolebinding\n", string(rr.Body.Bytes()))
}

func TestValidDeploymentRequestAndManifestCreateAlerts(t *testing.T) {
	appName := "appname"
	namespace := "default"
	fasitEnvironment := "environmentName"
	image := "name/Container"
	version := "123"
	alertName := "alias1"
	alertExpr := "db"

	clientset := fake.NewSimpleClientset()

	api := Api{clientset, "https://fasit.local", "nais.example.tk", "test-cluster", false, false, nil, fakeDeploymentHandler}

	depReq := naisrequest.Deploy{
		Application:      appName,
		Version:          version,
		FasitEnvironment: fasitEnvironment,
		ManifestUrl:      "http://repo.com/app",
		Zone:             "zone",
		Namespace:        namespace,
	}

	manifest := NaisManifest{
		Image: image,
		Port:  321,
		Team:  teamName,
		Alerts: []PrometheusAlertRule{
			{
				Alert: alertName,
				Expr:  alertExpr,
				For:   "5m",
				Annotations: map[string]string{
					"action": "alertAction",
				},
			},
		},
	}

	data, _ := yaml.Marshal(manifest)

	defer gock.Off()
	gock.New("https://fasit.local").
		Get("/api/v2/scopedresource").
		MatchParam("alias", NavTruststoreFasitAlias).
		Reply(200).File("testdata/fasitTruststoreResponse.json")

	gock.New("https://fasit.local").
		Get("/api/v2/resources/3024713/file/keystore").
		Reply(200).
		BodyString("")

	gock.New("http://repo.com").
		Get("/app").
		Reply(200).
		BodyString(string(data))

	jsn, _ := json.Marshal(depReq)

	body := strings.NewReader(string(jsn))

	req, _ := http.NewRequest("POST", "/deploy", body)

	rr := httptest.NewRecorder()
	handler := http.Handler(appHandler(api.deploy))

	handler.ServeHTTP(rr, req)

	assert.Equal(t, 200, rr.Code)
	assert.True(t, gock.IsDone())
	assert.Equal(t, "result: \n- created deployment\n- created secret\n- created service\n- created ingress\n- created autoscaler\n- updated alerts configmap (app-rules)\n- created serviceaccount\n- created rolebinding\n", string(rr.Body.Bytes()))
}

func TestThatFasitIsSkippedOnValidDeployment(t *testing.T) {
	appName := "appname"
	namespace := "default"
	image := "name/Container"
	version := "123"
	alertName := "alias1"
	alertExpr := "db"

	clientset := fake.NewSimpleClientset()

	api := Api{clientset, "https://fasit.local", "nais.example.tk", "test-cluster", false, false, nil, fakeDeploymentHandler}

	depReq := naisrequest.Deploy{
		Application: appName,
		Version:     version,
		ManifestUrl: "http://repo.com/app",
		SkipFasit:   true,
		Zone:        "zone",
		Namespace:   namespace,
	}

	manifest := NaisManifest{
		Image: image,
		Port:  321,
		Team:  teamName,
		Alerts: []PrometheusAlertRule{
			{
				Alert: alertName,
				Expr:  alertExpr,
				For:   "5m",
				Annotations: map[string]string{
					"action": "alertAction",
				},
			},
		},
	}

	data, _ := yaml.Marshal(manifest)

	defer gock.Off()
	gock.New("http://repo.com").
		Get("/app").
		Reply(200).
		BodyString(string(data))

	jsn, _ := json.Marshal(depReq)

	body := strings.NewReader(string(jsn))

	req, _ := http.NewRequest("POST", "/deploy", body)

	rr := httptest.NewRecorder()
	handler := http.Handler(appHandler(api.deploy))

	handler.ServeHTTP(rr, req)

	assert.Equal(t, 200, rr.Code)
	assert.True(t, gock.IsDone())
	assert.Equal(t, "result: \n- created deployment\n- created service\n- created ingress\n- created autoscaler\n- updated alerts configmap (app-rules)\n- created serviceaccount\n- created rolebinding\n", string(rr.Body.Bytes()))
}

func TestMissingResources(t *testing.T) {
	resourceAlias := "alias1"
	resourceType := "db"

	manifest := NaisManifest{
		Image: "name/Container",
		Port:  321,
		Team:  teamName,
		FasitResources: FasitResources{
			Used: []UsedResource{{resourceAlias, resourceType, nil}},
		},
	}
	data, _ := yaml.Marshal(manifest)

	defer gock.Off()
	gock.New("https://fasit.local").
		Get("/api/v2/scopedresource").
		MatchParam("alias", NavTruststoreFasitAlias).
		Reply(200).File("testdata/fasitResponse.json")
	gock.New("http://repo.com").
		Get("/app").
		Reply(200).
		BodyString(string(data))
	gock.New("https://fasit.local").
		Get("/api/v2/environments/fasitEnvironment").
		Reply(200).
		JSON(map[string]string{"environmentclass": "u"})
	gock.New("https://fasit.local").
		Get("/api/v2/applications/appname").
		Reply(200).
		BodyString("anything")
	gock.New("https://fasit.local").
		Get("/api/v2/scopedresource").
		Reply(404)

	req, _ := http.NewRequest("POST", "/deploy", strings.NewReader(CreateDefaultDeploymentRequest()))

	rr := httptest.NewRecorder()
	api := Api{fake.NewSimpleClientset(), "https://fasit.local", "nais.example.tk", "clustername", false, false, nil, fakeDeploymentHandler}
	handler := http.Handler(appHandler(api.deploy))

	handler.ServeHTTP(rr, req)

	assert.Equal(t, 400, rr.Code)
	assert.True(t, gock.IsDone())

	assert.Contains(t, string(rr.Body.Bytes()), fmt.Sprintf("unable to get resource %s (%s)", resourceAlias, resourceType))
}

func CreateDefaultDeploymentRequest() string {
	jsn, _ := json.Marshal(naisrequest.Deploy{
		Application:      "appname",
		Version:          "123",
		FasitEnvironment: "fasitEnvironment",
		ManifestUrl:      "http://repo.com/app",
		Zone:             "zone",
		Namespace:        "default",
	})

	return string(jsn)
}

func TestValidateDeploymentRequest(t *testing.T) {
	t.Run("Empty fields should be marked invalid", func(t *testing.T) {
		invalid := naisrequest.Deploy{
			Application:      "",
			Version:          "",
			FasitEnvironment: "",
			Zone:             "",
			Namespace:        "",
			FasitUsername:    "",
			FasitPassword:    "",
		}

		err := invalid.Validate()

		assert.NotNil(t, err)
		assert.Contains(t, err, errors.New("application is required and is empty"))
		assert.Contains(t, err, errors.New("version is required and is empty"))
		assert.Contains(t, err, errors.New("fasitEnvironment is required and is empty"))
		assert.Contains(t, err, errors.New("zone is required and is empty"))
		assert.Contains(t, err, errors.New("zone can only be fss, sbs or iapp"))
		assert.Contains(t, err, errors.New("namespace is required and is empty"))
		assert.Contains(t, err, errors.New("fasitUsername is required and is empty"))
		assert.Contains(t, err, errors.New("fasitPassword is required and is empty"))
	})

	t.Run("Fasit properties are not required when Fasit is skipped", func(t *testing.T) {
		invalid := naisrequest.Deploy{
			Application: "",
			Version:     "",
			Zone:        "",
			Namespace:   "",
			SkipFasit:   true,
		}

		err := invalid.Validate()

		assert.NotNil(t, err)
		assert.Len(t, err, 6)
		assert.Contains(t, err, errors.New("application is required and is empty"))
		assert.Contains(t, err, errors.New("invalid application name: a DNS-1123 label must consist of lower case alphanumeric characters or '-', and must start and end with an alphanumeric character (e.g. 'my-name',  or '123-abc', regex used for validation is '[a-z0-9]([-a-z0-9]*[a-z0-9])?')"))
		assert.Contains(t, err, errors.New("version is required and is empty"))
		assert.Contains(t, err, errors.New("zone is required and is empty"))
		assert.Contains(t, err, errors.New("zone can only be fss, sbs or iapp"))
		assert.Contains(t, err, errors.New("namespace is required and is empty"))
	})
	t.Run("Error generated when attempting to deploy to illegal namespace", func(t *testing.T) {
		illegal := naisrequest.Deploy{
			Application: "x",
			Version:     "1",
			Zone:        "fss",
			Namespace:   "kube-system",
			SkipFasit:   true,
		}

		err := illegal.Validate()

		assert.NotNil(t, err)
		assert.Len(t, err, 1)
		assert.Contains(t, err, errors.New("deploying to system namespaces disallowed"))
	})
}

func TestEnsurePropertyCompatibility(t *testing.T) {
	t.Run("Should warn when specifying environment", func(t *testing.T) {
		deploy := naisrequest.Deploy{
			Application: "application",
			Environment: "default",
		}

		warnings := ensurePropertyCompatibility(&deploy)
		response := createResponse(DeploymentResult{}, warnings)

		assert.Contains(t, string(response), "Specifying environment is deprecated, specify namespace instead.")
		assert.Len(t, warnings, 1)
	})
}
