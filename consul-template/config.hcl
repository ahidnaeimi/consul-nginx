consul {
  address = "consul:8500"
  token   = "29765957-1839-759e-8ed5-e44de35fcc2e"
}

template {
  source      = "/etc/consul-template/upstream.ctmpl"
  destination = "/etc/nginx/templates/upstream.conf"
  command     = ""
}