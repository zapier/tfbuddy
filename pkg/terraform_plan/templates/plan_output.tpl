{{ define "resource-change-content" }}
{{- range $key, $value := . }}
<li><code>{{ $key }}</code>
</li>
{{- end}}
{{ end }}

:seedling: <b>Additions:</b> {{.AdditionCount}}
<ul>
{{- range .Additions}}
    <li><code>{{ . }}</code></li>
{{- end}}
</ul>

:cyclone: <b>Changes:</b> {{.ChangeCount}}
<ul>
{{ template "resource-change-content" .Changes }}
</ul>

:recycle: <b>Replacements:</b> {{.ReplacementCount}}
<ul>
{{ template "resource-change-content" .Replacements }}
</ul>

:boom: <b>Destructions:</b> {{.DestructionCount}}
<ul>
{{- range .Destructions}}
<li><code>{{ . }}</code></li>
{{- end}}
</ul>
</br>
<b>Plan: </b> {{.AdditionCount}} to add, {{.ChangeCount}} to change, {{.ReplacementCount}} to replace and {{.DestructionCount}} to destroy.
</br>

See [Terraform Cloud Output]({{.TfcUrl}}) for more info.

