package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	volumes = []Volume{
		{
			Name:     "home",
			Path:     "/data/project",
			ReadOnly: false,
		},
		{
			Name:     "etc-ldap",
			Path:     "/etc/ldap",
			ReadOnly: true,
		},
	}
)

func decodeResponse(body io.ReadCloser) (*admissionv1.AdmissionReview, error) {
	response, _ := io.ReadAll(body)
	review := &admissionv1.AdmissionReview{}
	decoder := serializer.NewCodecFactory(runtime.NewScheme()).UniversalDeserializer()
	_, _, err := decoder.Decode(response, nil, review)
	return review, err
}

func encodeRequest(review *admissionv1.AdmissionReview) []byte {
	ret, err := json.Marshal(review)
	if err != nil {
		logrus.Errorln(err)
	}
	return ret
}

func getResponse(request admissionv1.AdmissionReview) (*admissionv1.AdmissionReview, error) {
	admission := &VolumeAdmission{
		Volumes: volumes,
	}

	server := httptest.NewServer(GetAdmissionControllerServerNoSsl(admission, ":8080").Handler)
	requestString := string(encodeRequest(&request))
	myr := strings.NewReader(requestString)
	r, _ := http.Post(server.URL, "application/json", myr)
	return decodeResponse(r.Body)
}

func getDummyRequest(namespace string, env []byte, volumeMounts []byte, volumes []byte) *admissionv1.AdmissionRequest {
	if namespace == "" {
		namespace = "tool-test"
	}
	header := []byte(`{
				"kind": "Pod",
				"apiVersion": "v1",
				"metadata": {
					"name": "maintain-kubeusers-123123123",
					"namespace": "maintain-kubeusers",
					"uid": "4b54be10-8d3c-11e9-8b7a-080027f5f85c",
					"creationTimestamp": "2019-06-12T18:02:51Z"
				},
				"spec": {`)
	if volumes == nil {
		volumes = []byte(`
					"volumes": [
						{
							"name": "home",
							"hostPath": {
								"path": "/data/project",
								"type": "Directory"
							}
						}
					],`)
	}
	containers_header := []byte(`
					"containers": [
						{
							"name": "maintain-kubeusers",
							"image": "docker-registry.tools.wmflabs.org/maintain-kubeusers:latest",
							"workingDir": "/some/path",
							"command": ["/app/venv/bin/python"],`)
	if env == nil {
		env = []byte(`
							"env": [
								{
									"name": "NO_HOME",
									"value": "original value"
								}
							],`)
	}
	if volumeMounts == nil {
		volumeMounts = []byte(`
							"volumeMounts": [
								{
									"mountPath": "/data/project",
									"name": "home"
								}
							],`)
	}
	containers_footer := []byte(`
							"args": ["/app/maintain_kubeusers.py"]
						}
					]`)
	footer := []byte(`
				}
			}`)
	return &admissionv1.AdmissionRequest{
		UID: "e911857d-c318-11e8-bbad-025000000001",
		Kind: v1.GroupVersionKind{
			Group: "", Version: "v1", Kind: "pod",
		},
		Operation: "CREATE",
		Namespace: namespace,
		Object: runtime.RawExtension{
			Raw: append(
				header, append(
					volumes, append(
						containers_header, append(
							env, append(
								volumeMounts, append(
									containers_footer,
									footer...)...)...)...)...)...),
		},
	}
}

func assertAllowedAndGetPatch(review *admissionv1.AdmissionReview, err error, t *testing.T) []PatchOperation {
	t.Log(review.Response)

	if err != nil {
		t.Error(err)
	}

	if !review.Response.Allowed {
		t.Error("Should not disallow tools with no HOME")
	}

	if *review.Response.PatchType != admissionv1.PatchTypeJSONPatch {
		t.Error("Wrong patch type found")
	}

	var p []PatchOperation
	err = json.Unmarshal(review.Response.Patch, &p)
	if err != nil {
		t.Error(err)
	}
	return p
}

func TestServerIgnoresNonToolPods(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{Kind: "AdmissionReview"},
		Request:  getDummyRequest("maintain-kubeusers", []byte(`"env": [],`), []byte(``), []byte(``)),
	})

	t.Log(review.Response)

	if err != nil {
		t.Error(err)
	}

	if review.Response.Allowed {
		t.Error("Should not allow non-tools")
	}

	if review.Response.Patch != nil {
		t.Error("Should not contain patch when not allowed")
	}
}

func TestServerMountsAllVolumesWhenNoneExist(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{
			Kind: "AdmissionReview",
		},
		Request: &admissionv1.AdmissionRequest{
			UID: "e911857d-c318-11e8-bbad-025000000001",
			Kind: v1.GroupVersionKind{
				Group: "", Version: "v1", Kind: "pod",
			},
			Operation: "CREATE",
			Namespace: "tool-test",
			Object: runtime.RawExtension{
				Raw: []byte(`{
					"kind": "Pod",
					"apiVersion": "v1",
					"metadata": {
						"name": "test-123123123",
						"namespace": "tool-test",
						"uid": "4b54be10-8d3c-11e9-8b7a-080027f5f85c",
						"creationTimestamp": "2019-06-12T18:02:51Z"
					},
					"spec": {
						"nodeSelector": {
							"foo": "bar"
						},
						"containers": [
							{
								"name": "test",
								"image": "docker-registry.tools.wmflabs.org/toolforge-python39-web:latest",
								"command": ["/usr/bin/webservice-runner"],
								"args": ["python39"]
							}
						]
					}
				}`),
			},
		},
	})

	var p = assertAllowedAndGetPatch(review, err, t)

	if len(p) != 10 {
		t.Errorf("Patch length %d does not match expected value 10", len(p))
	}
}

func TestServerMountsAllVolumesIfSomeExist(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{Kind: "AdmissionReview"},
		Request: getDummyRequest("", []byte(`"env": [],`), []byte(`
								"volumeMounts": [
									{
										"mountPath": "/var/run/secrets/kubernetes.io/serviceaccount",
										"name": "default-token-abcde",
										"readOnly": true
									}
								],`), []byte(`"volumes": [
							{
								"name": "default-token-abcde",
								"secret": {
									"defaultMode": 420,
									"secretName": "default-token-abcde"
								}
							}
						],`))})

	var p = assertAllowedAndGetPatch(review, err, t)

	if len(p) != 8 {
		t.Errorf("Patch length %d does not match expected value of 8, got patches:\n%s", len(p), p)
	}
}

func TestServerMountsNeededVolumes(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{Kind: "AdmissionReview"},
		Request: getDummyRequest("", []byte(`
								"env": [
									{
										"name": "HOME",
										"value": "/foobar"
									}
								],`), nil, nil),
	})

	var p = assertAllowedAndGetPatch(review, err, t)

	if len(p) != 5 {
		t.Errorf("Patch length %d does not match expected value of 5", len(p))
	}
}

func TestServerSetsHOMEIfNotSet(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{Kind: "AdmissionReview"},
		Request:  getDummyRequest("", []byte(``), nil, nil),
	})

	var p = assertAllowedAndGetPatch(review, err, t)

	addHomeFound := false

	r, _ := regexp.Compile("/spec/containers/[0-9]*/env/-")
	for _, patch := range p {
		match := r.Match([]byte(patch.Path))
		if match && patch.Value.(map[string]interface{})["name"] == "HOME" {
			addHomeFound = true
			break
		}
	}

	if !addHomeFound {
		t.Errorf("Did not find a patch that added the HOME env variable")
	}
}

func TestServerDoesNotChangeHOMEIfSet(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{Kind: "AdmissionReview"},
		Request: getDummyRequest("", []byte(`
										"env": [
											{
												"name": "HOME",
												"value": "original value"
											}
										],`), nil, nil),
	})

	var p = assertAllowedAndGetPatch(review, err, t)

	r, _ := regexp.Compile("/spec/containers/[0-9]*/env/-")
	for _, patch := range p {
		match := r.Match([]byte(patch.Path))
		if match && patch.Value.(map[string]interface{})["name"] == "HOME" {
			t.Errorf("Found a patch that adds/removes/replaces the HOME variable: %s", patch)
		}
	}
}

func TestServerDoesNotAddHOMEifNO_HOMESet(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{
			Kind: "AdmissionReview",
		},
		Request: getDummyRequest("", []byte(`"env": [
			{
				"name": "NO_HOME",
				"value": "dummy"
			}
		],`), nil, nil),
	})

	var p = assertAllowedAndGetPatch(review, err, t)

	r, _ := regexp.Compile("/spec/containers/[0-9]*/env/-")
	for _, patch := range p {
		match := r.Match([]byte(patch.Path))
		if match && patch.Value.(map[string]interface{})["name"] == "HOME" {
			t.Errorf("Found a patch that adds/removes/replaces the HOME variable: %s", patch)
		}
	}
}

func TestServerRemovesWorkingDirIfNO_HOMESet(t *testing.T) {
	review, err := getResponse(admissionv1.AdmissionReview{
		TypeMeta: v1.TypeMeta{
			Kind: "AdmissionReview",
		},
		Request: getDummyRequest("", []byte(`"env": [
			{
				"name": "NO_HOME",
				"value": "dummy"
			}
		],`), nil, nil),
	})

	var p = assertAllowedAndGetPatch(review, err, t)

	removeWorkingDirFound := false
	r, _ := regexp.Compile("/spec/containers/[0-9]*/workingDir")
	for _, patch := range p {
		match := r.Match([]byte(patch.Path))
		if match && patch.Op == "remove" {
			removeWorkingDirFound = true
			break
		}
	}

	if !removeWorkingDirFound {
		t.Errorf("Did not find a patch that removed the WorkingDir entry among %s", p)
	}
}
