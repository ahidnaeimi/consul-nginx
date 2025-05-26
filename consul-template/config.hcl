consul {
  address = "consul:8500"
  token   = "eb5a4d02-c47b-014c-d2b0-8cc0a52fdc79"
}

template {
  source      = "/etc/consul-template/upstream.ctmpl"
  destination = "/etc/nginx/templates/upstream.conf"
  command     = ""
}