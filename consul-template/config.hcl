consul {
  address = "consul:8500"
  token   = "ca01e2c4-f6c1-d582-6ba3-fc5f63208ded"
}

template {
  source      = "/etc/consul-template/upstream.ctmpl"
  destination = "/etc/nginx/templates/upstream.conf"
  command     = ""
}