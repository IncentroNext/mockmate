variable "project" {
}

variable "region" {
}

variable "invokers" {
  type = list(string)
}

variable "service_version" {
  description = "version of the service"
}

variable "image" {
  description = "container image"
}

variable release {
  default = "v1"
  description = "release version label"
}
