apiVersion: apps/v1
kind: Deployment
metadata:
  name: volume-admission
  labels:
    name: volume-admission
  annotations:
    secret.reloader.stakater.com/reload: "{{ .Values.webhook.secretName }}"
spec:
  replicas: 2
  selector:
    matchLabels:
      name: volume-admission
  template:
    metadata:
      name: volume-admission
      labels:
        name: volume-admission
      annotations:
        # ensure the pod gets restarted when config changes
        volume-admission.tools.wmcloud.org/config-checksum: {{ .Values.volumes | toJson | sha256sum }}
    spec:
      topologySpreadConstraints:
        - maxSkew: 1
          topologyKey: kubernetes.io/hostname
          whenUnsatisfiable: ScheduleAnyway
          labelSelector:
            matchLabels:
              name: volume-admission
      containers:
        - name: webhook
          image: "{{ .Values.image.name }}:{{ .Values.image.tag }}"
          imagePullPolicy: "{{ .Values.image.pullPolicy }}"
          env:
            - name: "DEBUG"
              value: "{{ .Values.debug }}"
          resources: {{- toYaml .Values.resources | nindent 12 }}
          volumeMounts:
            - name: webhook-certs
              mountPath: /etc/webhook/certs
              readOnly: true
            - name: volumes-config
              mountPath: /etc/volumes.json
              subPath: volumes.json
              readOnly: true
          securityContext:
            readOnlyRootFilesystem: true
      volumes:
        - name: webhook-certs
          secret:
            secretName: "{{ .Values.webhook.secretName }}"
        - name: volumes-config
          configMap:
            name: volumes-config
            items:
              - key: volumes.json
                path: volumes.json
