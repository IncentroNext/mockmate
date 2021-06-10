variable "project" {
}

variable "proj_hash" {
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

variable "envs" {
  type = map(string)
  description = "service environment variables"
}

variable release {
  default = "v1"
  description = "release version label"
}