{{- define "xeol-operator.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "xeol-operator.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{- define "xeol-operator.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{ include "xeol-operator.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "xeol-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "xeol-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* DB dir the operator reads: the mount path in mounted mode, an emptyDir otherwise. */}}
{{- define "xeol-operator.dbDir" -}}
{{- if eq .Values.db.source "mounted" }}{{ .Values.db.mounted.path }}{{ else }}/var/cache/xeol{{ end -}}
{{- end -}}
