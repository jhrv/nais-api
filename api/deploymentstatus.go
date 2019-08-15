package api

import (
	"fmt"
	"github.com/golang/glog"
	"github.com/nais/naisd/api/app"
	k8score "k8s.io/api/core/v1"
	k8sapps "k8s.io/api/apps/v1"
	k8smeta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type DeployStatus int

func (d DeployStatus) String() string {
	switch d {
	case InProgress:
		return "InProgress"
	case Failed:
		return "Failed"
	case Success:
		return "Success"
	default:
		return ""
	}
}

const (
	Success DeployStatus = iota
	InProgress
	Failed
)

type DeploymentStatusViewer interface {
	DeploymentStatusView(namespace, deployName string) (DeployStatus, DeploymentStatusView, error)
}

type deploymentStatusViewerImpl struct {
	client kubernetes.Interface
}

func NewDeploymentStatusViewer(clientset kubernetes.Interface) DeploymentStatusViewer {
	return &deploymentStatusViewerImpl{
		clientset,
	}
}

func (d deploymentStatusViewerImpl) DeploymentStatusView(namespace, deployName string) (DeployStatus, DeploymentStatusView, error) {
	// First, check new namespace
	spec := app.Spec{Application: deployName, Namespace: namespace}
	dep, err := d.client.AppsV1().Deployments(spec.Namespace).Get(spec.ResourceName(), k8smeta.GetOptions{})
	if err != nil {
		errMess := fmt.Sprintf("did not find deployment: %s namespace: %s", deployName, namespace)
		glog.Error(errMess)
		return Failed, DeploymentStatusView{}, fmt.Errorf("did not find deployment: %s namespace: %s", deployName, namespace)
	}

	status, view := deploymentStatusAndView(*dep)
	return status, view, nil

}

type DeploymentStatusView struct {
	Name       string
	Desired    int32
	Current    int32
	UpToDate   int32
	Available  int32
	Containers []string
	Images     []string
	Status     string
	Reason     string
}

func deploymentStatusViewFrom(status DeployStatus, reason string, deployment k8sapps.Deployment) DeploymentStatusView {
	containers, images := findContainerImages(deployment.Spec.Template.Spec.Containers)

	return DeploymentStatusView{
		Name:       deployment.Name,
		Desired:    *deployment.Spec.Replicas,
		Current:    deployment.Status.Replicas,
		UpToDate:   deployment.Status.UpdatedReplicas,
		Available:  deployment.Status.AvailableReplicas,
		Containers: containers,
		Images:     images,
		Status:     status.String(),
		Reason:     reason,
	}

}

func findContainerImages(containers []k8score.Container) ([]string, []string) {
	names, images := []string{}, []string{}

	for _, container := range containers {
		names = append(names, container.Name)
		images = append(images, container.Image)
	}
	return names, images
}

func deploymentStatusAndView(deployment k8sapps.Deployment) (DeployStatus, DeploymentStatusView) {
	if deployment.Generation <= deployment.Status.ObservedGeneration {
		switch {

		case deploymentExceededProgressDeadline(deployment.Status):
			reason := fmt.Sprintf("deployment %s exceeded its progress deadline", deployment.Name)
			return Failed, deploymentStatusViewFrom(Failed, reason, deployment)

		case deployment.Status.UpdatedReplicas < *deployment.Spec.Replicas:
			reason := fmt.Sprintf("Waiting for rollout to finish: %d out of %d new replicas have been updated.", deployment.Status.UpdatedReplicas, deployment.Spec.Replicas)
			return InProgress, deploymentStatusViewFrom(InProgress, reason, deployment)

		case deployment.Status.Replicas > deployment.Status.UpdatedReplicas:
			reason := fmt.Sprintf("Waiting for rollout to finish: %d old replicas are pending termination.", deployment.Status.Replicas-deployment.Status.UpdatedReplicas)
			return InProgress, deploymentStatusViewFrom(InProgress, reason, deployment)

		case deployment.Status.AvailableReplicas < deployment.Status.UpdatedReplicas:
			reason := fmt.Sprintf("Waiting for rollout to finish: %d of %d updated replicas are available.", deployment.Status.AvailableReplicas, deployment.Status.UpdatedReplicas)
			return InProgress, deploymentStatusViewFrom(InProgress, reason, deployment)

		default:
			reason := fmt.Sprintf("deployment %q successfully rolled out.", deployment.Name)
			return Success, deploymentStatusViewFrom(Success, reason, deployment)

		}

	}
	return InProgress, deploymentStatusViewFrom(InProgress, "Waiting for deployment spec update to be observed", deployment)
}

func deploymentExceededProgressDeadline(status k8sapps.DeploymentStatus) bool {
	for i := range status.Conditions {
		c := status.Conditions[i]
		if c.Type == k8sapps.DeploymentProgressing && c.Reason == "ProgressDeadlineExceeded" {
			return true
		}
	}
	return false
}
