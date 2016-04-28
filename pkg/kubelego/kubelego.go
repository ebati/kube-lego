package kubelego

import (
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"sync"

	"github.com/simonswine/kube-lego/pkg/acme"

	log "github.com/Sirupsen/logrus"
	legoAcme "github.com/xenolf/lego/acme"
	k8sApi "k8s.io/kubernetes/pkg/api"
	k8sClient "k8s.io/kubernetes/pkg/client/unversioned"
	"k8s.io/kubernetes/pkg/util/intstr"
	"github.com/simonswine/kube-lego/pkg/kubelego_const"
)

func New(version string) *KubeLego {
	return &KubeLego{
		version: version,
		stopCh:  make(chan struct{}),
		waitGroup: sync.WaitGroup{},
	}
}

func (kl *KubeLego) Log() *log.Entry {
	log.SetLevel(log.DebugLevel)
	return log.WithField("context", "kubelego")
}

func (kl *KubeLego) Stop(){
	kl.Log().Info("shuting things down")
	close(kl.stopCh)
}

func (kl *KubeLego) Init() {
	kl.Log().Infof("kube-lego %s starting", kl.version)

	// handle sigterm correctly
	k := make(chan os.Signal, 1)
	signal.Notify(k, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		s := <-k
		logger := kl.Log().WithField("signal", s.String())
		logger.Debug("received signal")
		kl.Stop()
	}()

	// parse env vars
	err := kl.paramsLego()
	if err != nil {
		kl.Log().Fatal(err)
	}

	// start workers
	kl.WatchReconfigure()

	// intialize kube api
	err = kl.InitKube()
	if err != nil {
		kl.Log().Fatal(err)
	}

	err = kl.InitLego()
	if err != nil {
		kl.Log().Fatal(err)
	}

	// run acme http server
	myAcme := acme.New(kl)
	go func() {
		kl.waitGroup.Add(1)
		defer kl.waitGroup.Done()
		myAcme.RunServer(kl.stopCh)
	}()

	// watch for ingress controller events
	kl.WatchEvents()

	<-kl.stopCh
	kl.Log().Infof("exiting")
	kl.waitGroup.Wait()
}

func (kl *KubeLego) KubeClient() *k8sClient.Client {
	return kl.kubeClient
}

func (kl *KubeLego) LegoClient() *legoAcme.Client {
	return kl.legoClient
}

func (kl *KubeLego) Version() string {
	return kl.version
}

func (kl *KubeLego) LegoHTTPPort() string {
	return fmt.Sprintf(":%d", kl.legoHTTPPort.IntValue())
}

func (kl *KubeLego) LegoURL() string {
	return kl.legoURL
}

func (kl *KubeLego) LegoEmail() string {
	return kl.legoEmail
}

type mockAcme struct {

}
func (mA *mockAcme) ObtainCertificate([]string) (data map[string][]byte,err error){
	return
}

func (kl *KubeLego) AcmeClient() kubelego.Acme{
	return &mockAcme{}
}

// read config parameters from ENV vars
func (kl *KubeLego) paramsLego() error {

	kl.legoEmail = os.Getenv("LEGO_EMAIL")
	if len(kl.legoEmail) == 0 {
		return errors.New("Please provide an email address for cert recovery in LEGO_EMAIL")
	}

	kl.LegoNamespace = os.Getenv("LEGO_NAMESPACE")
	if len(kl.LegoNamespace) == 0 {
		kl.LegoNamespace = k8sApi.NamespaceDefault
	}

	kl.legoURL = os.Getenv("LEGO_URL")
	if len(kl.legoURL) == 0 {
		kl.legoURL = "https://acme-staging.api.letsencrypt.org/directory"
	}

	kl.LegoSecretName = os.Getenv("LEGO_SECRET_NAME")
	if len(kl.LegoSecretName) == 0 {
		kl.LegoSecretName = "kube-lego-account"
	}

	kl.LegoServiceName = os.Getenv("LEGO_SERVICE_NAME")
	if len(kl.LegoServiceName) == 0 {
		kl.LegoServiceName = "kube-lego"
	}

	kl.LegoIngressName = os.Getenv("LEGO_INGRESS_NAME")
	if len(kl.LegoIngressName) == 0 {
		kl.LegoIngressName = "kube-lego"
	}

	httpPortStr := os.Getenv("LEGO_PORT")
	if len(httpPortStr) == 0 {
		kl.legoHTTPPort = intstr.FromInt(8080)
	} else {
		i, err := strconv.Atoi(httpPortStr)
		if err != nil {
			return err
		}
		if i <= 0 || i >= 65535 {
			return fmt.Errorf("Wrong port: %d", i)
		}
		kl.legoHTTPPort = intstr.FromInt(i)

	}

	return nil
}