data "phala_images" "west" {
  region = "US-WEST-1"
}

output "image_slugs" {
  value = data.phala_images.west.images[*].slug
}
