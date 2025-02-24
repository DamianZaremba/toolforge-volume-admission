package server

import (
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/json"
)

const (
	// MountConfigLabel is the label that can be used to configure
	// which NFS volumes to mount in a pod.
	MountConfigLabel = "toolforge.org/mount-storage"

	// MountAll is the option for pods with all the available volumes
	// mounted.
	MountAll = "all"
	// MountNone is the option for pods with no volumes mounted.
	MountNone = "none"
)

// PatchOperation describes an operation done to modify a Kubernetes
// resource
type PatchOperation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

// Volume contains details about one specific volume mounted to
// Toolforge Kubernetes containers
type Volume struct {
	Name     string              `json:"name"`
	Path     string              `json:"path"`
	Type     corev1.HostPathType `json:"type"`
	ReadOnly bool                `json:"readOnly"`
}

// VolumeAdmission type is what does all the magic
type VolumeAdmission struct {
	Volumes []Volume
}

func getLabelOrDefault(pod corev1.Pod, label string, defaultValue string) string {
	value, exists := pod.ObjectMeta.Labels[label]
	if exists {
		return value
	}

	return defaultValue
}

func hasMountByPath(container corev1.Container, path string) bool {
	for _, mount := range container.VolumeMounts {
		if mount.MountPath == path {
			return true
		}
	}

	return false
}

func hasVolumeByName(pod corev1.Pod, name string) bool {
	for _, volume := range pod.Spec.Volumes {
		if volume.Name == name {
			return true
		}
	}

	return false
}

func hasEnvVarSet(container *corev1.Container, envVar string) bool {
	for _, env := range container.Env {
		if env.Name == envVar {
			return true
		}
	}
	return false
}

// HandleAdmission has all the webhook logic to possibly mount volumes
// to containers if needed
func (admission *VolumeAdmission) HandleAdmission(review *admissionv1.AdmissionReview) {
	req := review.Request

	var pod corev1.Pod
	err := json.Unmarshal(req.Object.Raw, &pod)
	if err != nil {
		logrus.Errorf("Could not unmarshal raw object: %v", err)
		review.Response = &admissionv1.AdmissionResponse{
			UID: review.Request.UID,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}

		return
	}

	logrus.Debugf("AdmissionReview for Kind=%v, Namespace=%v Name=%v (%v) UID=%v patchOperation=%v UserInfo=%v",
		req.Kind, req.Namespace, req.Name, pod.Name, req.UID, req.Operation, req.UserInfo)

	mountConfig := getLabelOrDefault(pod, MountConfigLabel, MountAll)
	if mountConfig != MountAll && mountConfig != MountNone {
		review.Response = &admissionv1.AdmissionResponse{
			UID:     review.Request.UID,
			Allowed: false,
			Result: &metav1.Status{
				Message: fmt.Sprintf("Invalid value for %v label", MountConfigLabel),
			},
		}

		return
	}

	if !strings.HasPrefix(req.Namespace, "tool-") {
		logrus.Warningf("Skipping non-tool namespace %v", req.Namespace)

		review.Response = &admissionv1.AdmissionResponse{
			UID: review.Request.UID,
			Result: &metav1.Status{
				Message: "Only tools can have tool volumes mounted to them",
			},
		}

		return
	}

	var patches []PatchOperation

	// Deny the request if we find any hostPath volume, as we only allow
	// toolforge managed ones
	if len(pod.Spec.Volumes) != 0 {
		for idx, volume := range pod.Spec.Volumes {
			if volume.HostPath != nil {
				// home might be added already, so skip if it's ok
				// note that we mount all the tools homes as tools should be able to read from others
				if volume.Name != "home" || volume.HostPath.Path != "/data/project" {
					review.Response = &admissionv1.AdmissionResponse{
						UID:     review.Request.UID,
						Allowed: false,
						Result: &metav1.Status{
							Message: fmt.Sprintf(
								"No hostPath volumes allowed, got one under /spec/volumes/%d Name:%s HostPath:%s",
								idx,
								volume.Name,
								volume.HostPath,
							),
						},
					}
					return
				}
			}
		}
	} else {
		// initialize if it's not there
		patches = append(patches, PatchOperation{
			Op:    "add",
			Path:  "/spec/volumes",
			Value: []string{},
		})
	}

	if mountConfig == MountNone {
		patchType := admissionv1.PatchTypeJSONPatch
		response := &admissionv1.AdmissionResponse{
			UID:       review.Request.UID,
			PatchType: &patchType,
			Allowed:   true,
			Result: &metav1.Status{
				Message: "No volumes requested",
			},
		}

		response.Patch, err = json.Marshal(patches)
		if err != nil {
			logrus.Errorf("Could not marshal patch object: %v", err)
			review.Response = &admissionv1.AdmissionResponse{
				UID: review.Request.UID,
				Result: &metav1.Status{
					Message: err.Error(),
				},
			}
		}

		review.Response = response
		return
	}

	for i, container := range pod.Spec.Containers {
		// If there are no volumesMounts, json-patch will fail
		// unless we add it with an op.
		if len(container.VolumeMounts) == 0 {
			patch := PatchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/volumeMounts", i),
				Value: []string{},
			}
			patches = append(patches, patch)
		}
	}

	for _, volume := range admission.Volumes {
		if hasVolumeByName(pod, volume.Name) {
			continue
		}

		var volumeType = volume.Type
		patch := PatchOperation{
			Op:   "add",
			Path: "/spec/volumes/-",
			Value: &corev1.Volume{
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: volume.Path,
						Type: &volumeType,
					},
				},
				Name: volume.Name,
			},
		}
		patches = append(patches, patch)

		for i, container := range pod.Spec.Containers {
			// Ignore pods that already have this volume mounted
			if hasMountByPath(container, volume.Path) {
				continue
			}

			patch := PatchOperation{
				Op:   "add",
				Path: fmt.Sprintf("/spec/containers/%d/volumeMounts/-", i),
				Value: &corev1.VolumeMount{
					MountPath: volume.Path,
					Name:      volume.Name,
					ReadOnly:  volume.ReadOnly,
				},
			}
			patches = append(patches, patch)
		}
	}

	for i, container := range pod.Spec.Containers {
		// Initialize the env entry itself, otherwise further patches will fail
		if container.Env == nil {
			patch := PatchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/env", i),
				Value: []corev1.EnvVar{},
			}
			patches = append(patches, patch)
		}

		// If $HOME is already set don't overwrite it
		skipSettingHome := false
		if hasEnvVarSet(&container, "HOME") {
			skipSettingHome = true
		}

		// If $NO_HOME is set, don't add any HOME, and remove any workingDir to let the image decide
		if hasEnvVarSet(&container, "NO_HOME") {
			skipSettingHome = true
			if container.WorkingDir != "" {
				patch := PatchOperation{
					Op:   "remove",
					Path: fmt.Sprintf("/spec/containers/%d/workingDir", i),
				}
				patches = append(patches, patch)
			}
		}

		toolName := strings.Replace(req.Namespace, "tool-", "", 1)
		toolHome := fmt.Sprintf("/data/project/%v", toolName)
		if !skipSettingHome {
			patch := PatchOperation{
				Op:    "add",
				Path:  fmt.Sprintf("/spec/containers/%d/env/-", i),
				Value: &corev1.EnvVar{Name: "HOME", Value: toolHome},
			}
			patches = append(patches, patch)
		}

		// Always add the TOOL_DATA_DIR env var
		patch := PatchOperation{
			Op:    "add",
			Path:  fmt.Sprintf("/spec/containers/%d/env/-", i),
			Value: &corev1.EnvVar{Name: "TOOL_DATA_DIR", Value: toolHome},
		}
		patches = append(patches, patch)

	}

	if pod.Spec.NodeSelector == nil {
		pod.Spec.NodeSelector = map[string]string{}
		patch := PatchOperation{
			Op:    "add",
			Path:  "/spec/nodeSelector",
			Value: map[string]string{},
		}

		patches = append(patches, patch)
	}

	if _, exists := pod.Spec.NodeSelector["kubernetes.wmcloud.org/nfs-mounted"]; !exists {
		patch := PatchOperation{
			Op:    "add",
			Path:  "/spec/nodeSelector/kubernetes.wmcloud.org~1nfs-mounted",
			Value: "true",
		}

		patches = append(patches, patch)
	}

	patchType := admissionv1.PatchTypeJSONPatch

	response := &admissionv1.AdmissionResponse{
		UID:       review.Request.UID,
		PatchType: &patchType,
		Allowed:   true,
		Result: &metav1.Status{
			Message: "Volumes mounted",
		},
	}

	// parse the []map into JSON
	response.Patch, err = json.Marshal(patches)
	if err != nil {
		logrus.Errorf("Could not marshal patch object: %v", err)
		review.Response = &admissionv1.AdmissionResponse{
			UID: review.Request.UID,
			Result: &metav1.Status{
				Message: err.Error(),
			},
		}

		return
	}

	review.Response = response
}
