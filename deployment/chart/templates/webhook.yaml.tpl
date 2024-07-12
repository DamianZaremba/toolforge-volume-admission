apiVersion: admissionregistration.k8s.io/v1
kind: MutatingWebhookConfiguration
metadata:
  name: volume-admission
  annotations:
    cert-manager.io/inject-ca-from: "{{ .Release.Namespace }}/{{ .Values.webhook.secretName }}"
webhooks:
  - name: volume-admission.tools.wmcloud.org
    clientConfig:
      service:
        name: volume-admission
        namespace: "{{ .Release.Namespace }}"
        path: "/"
      caBundle: ""  # injected by cert-manager
    failurePolicy: "{{ .Values.webhook.failurePolicy }}"
    matchPolicy: Equivalent
    sideEffects: None
    admissionReviewVersions: ["v1"]
    objectSelector:
      {{ .Values.webhook.objectSelector | toYaml | nindent 6 }}
    rules:
      # only CREATE and not UPDATE, because most fields in a Pod are immutable
      # see https://kubernetes.io/docs/concepts/workloads/pods/#pod-update-and-replacement
      - operations: ["CREATE"]
        apiGroups: [""]
        apiVersions: ["v1"]
        resources: ["pods"]
