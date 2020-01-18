package installer

import (
	"context"
	"errors"
	"fmt"
	"io"

	kubeutils "github.com/nicolaferraro/snap/pkg/util/kubernetes"
	"github.com/nicolaferraro/snap/pkg/util/log"
	"github.com/nicolaferraro/snap/pkg/util/vsf"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	restclient "k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	logger = log.WithName("installer")

	serverLabels = map[string]string{
		"snap.io/component": "server",
	}
)

type Installer struct {
	config *restclient.Config
	client ctrl.Client
	stdOut io.Writer
	stdErr io.Writer
}

func NewInstaller(config *restclient.Config, client ctrl.Client, stdOut, stdErr io.Writer) *Installer {
	return &Installer{
		config: config,
		client: client,
		stdOut: stdOut,
		stdErr: stdErr,
	}
}

func (i *Installer) isInstalled(ctx context.Context, ns string) (bool, error) {
	deploymentList := appsv1.DeploymentList{}
	if err := i.client.List(ctx, &deploymentList, ctrl.InNamespace(ns), ctrl.MatchingLabels(serverLabels)); err != nil {
		return false, err
	}
	return len(deploymentList.Items) > 0, nil
}

func (i *Installer) OpenConnection(ctx context.Context, ns string, direct bool) (string, error) {
	if direct {
		return i.GetDirectConnectionHost(ctx, ns)
	}

	logger.Info("Waiting for destination pod to be ready...")
	pod, err := kubeutils.WaitForPodReady(ctx, i.client, ns, serverLabels)
	if err != nil {
		return "", err
	} else if pod == "" {
		return "", errors.New("cannot find server pod")
	}

	logger.Infof("Opening connection to pod %s", pod)
	host, err := kubeutils.PortForward(ctx, i.config, ns, pod, i.stdOut, i.stdErr)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s", host), nil
}

func (i *Installer) GetDirectConnectionHost(ctx context.Context, ns string) (string, error) {
	serviceList := corev1.ServiceList{}
	if err := i.client.List(ctx, &serviceList, ctrl.InNamespace(ns), ctrl.MatchingLabels(serverLabels)); err != nil {
		return "", err
	}
	if len(serviceList.Items) == 0 {
		return "", errors.New("no snap server found")
	}
	return fmt.Sprintf("%s:9000", serviceList.Items[0].Name), nil
}

func (i *Installer) EnsureInstalled(ctx context.Context, ns string) error {
	if installed, err := i.isInstalled(ctx, ns); err != nil {
		return err
	} else if installed {
		logger.Info("Snap is already installed: skipping")
		return nil
	}

	logger.Infof("Installing Snap into the %s namespace...", ns)

	if err := i.installResource(ctx, ns, "/minio-standalone-pvc.yaml"); err != nil {
		return err
	}
	if err := i.installResource(ctx, ns, "/minio-standalone-deployment.yaml"); err != nil {
		return err
	}
	if err := i.installResource(ctx, ns, "/minio-standalone-service.yaml"); err != nil {
		return err
	}
	logger.Infof("Installation complete in namespace %s", ns)
	return nil
}

func (i *Installer) installResource(ctx context.Context, ns string, name string) error {
	pvc, err := kubeutils.LoadResourceFromYamlFile(scheme.Scheme, name, vsf.LoadAsString)
	if err != nil {
		return err
	}

	return kubeutils.ReplaceResourceInNamespace(ctx, i.client, pvc, ns)
}