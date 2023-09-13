package server

import (
	"crypto/tls"
	"io"
	"net/http"

	"github.com/sirupsen/logrus"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/json"
)

// AdmissionController is an abstraction to work with the admission handler
type AdmissionController interface {
	HandleAdmission(review *admissionv1.AdmissionReview)
}

// AdmissionControllerServer combines a decoder with an AdmissionController
type AdmissionControllerServer struct {
	AdmissionController AdmissionController
	Decoder             runtime.Decoder
}

func (acs *AdmissionControllerServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body []byte
	if data, err := io.ReadAll(r.Body); err == nil {
		body = data
	}

	logrus.Debugln(string(body))

	review := &admissionv1.AdmissionReview{}
	_, _, err := acs.Decoder.Decode(body, nil, review)
	if err != nil {
		logrus.Errorln("Can't decode request", err)
	}

	acs.AdmissionController.HandleAdmission(review)
	responseInBytes, err := json.Marshal(review)
	if err != nil {
		logrus.Errorln("Failed to convert response to JSON", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(responseInBytes); err != nil {
		logrus.Errorln("Failed to write response", err)
	}
}

// GetAdmissionControllerServerNoSsl creates a web server that serves
// Kubernetes webhook requests
func GetAdmissionControllerServerNoSsl(ac AdmissionController, listenOn string) *http.Server {
	return &http.Server{
		Handler: &AdmissionControllerServer{
			AdmissionController: ac,
			Decoder:             serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer(),
		},
		Addr: listenOn,
	}
}

// GetAdmissionControllerServer creates a web server that serves
// Kubernetes webhook requests with TLS
func GetAdmissionControllerServer(ac AdmissionController, tlsCert string, tlsKey string, listenOn string) (*http.Server, error) {
	certificate, err := tls.LoadX509KeyPair(tlsCert, tlsKey)
	if err != nil {
		return nil, err
	}

	server := GetAdmissionControllerServerNoSsl(ac, listenOn)
	server.TLSConfig = &tls.Config{
		Certificates: []tls.Certificate{certificate},
	}

	return server, nil
}
