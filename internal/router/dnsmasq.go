package router

import (
	"strings"
	"text/template"
)

const dnsMasqConfigContentTmpl = `# GENERATED BY ctrld - DO NOT MODIFY
no-resolv
server=127.0.0.1#5354
{{- if .SendClientInfo}}
add-mac
{{- end}}
`

const merlinDNSMasqPostConfPath = "/jffs/scripts/dnsmasq.postconf"
const merlinDNSMasqPostConfMarker = `# GENERATED BY ctrld - EOF`

const merlinDNSMasqPostConfTmpl = `# GENERATED BY ctrld - DO NOT MODIFY

#!/bin/sh

config_file="$1"
. /usr/sbin/helper.sh

pid=$(cat /tmp/ctrld.pid 2>/dev/null)
if [ -n "$pid" ] && [ -f "/proc/${pid}/cmdline" ]; then
  pc_delete "servers-file" "$config_file"           # no WAN DNS settings
  pc_append "no-resolv" "$config_file"              # do not read /etc/resolv.conf
  pc_append "server=127.0.0.1#5354" "$config_file"  # use ctrld as upstream
  {{- if .SendClientInfo}}
  pc_append "add-mac" "$config_file"                # add client mac
  {{- end}}
  pc_delete "dnssec" "$config_file"                 # disable DNSSEC
  pc_delete "trust-anchor=" "$config_file"          # disable DNSSEC
	
  # For John fork
  pc_delete "resolv-file" "$config_file"            # no WAN DNS settings

  # Change /etc/resolv.conf, which may be changed by WAN DNS setup
  pc_delete "nameserver" /etc/resolv.conf
  pc_append "nameserver 127.0.0.1" /etc/resolv.conf

  exit 0
fi
`

func dnsMasqConf() (string, error) {
	var sb strings.Builder
	var tmplText string
	switch Name() {
	case DDWrt, OpenWrt, Ubios:
		tmplText = dnsMasqConfigContentTmpl
	case Merlin:
		tmplText = merlinDNSMasqPostConfTmpl
	}
	tmpl := template.Must(template.New("").Parse(tmplText))
	var to = &struct {
		SendClientInfo bool
	}{
		routerPlatform.Load().sendClientInfo,
	}
	if err := tmpl.Execute(&sb, to); err != nil {
		return "", err
	}
	return sb.String(), nil
}