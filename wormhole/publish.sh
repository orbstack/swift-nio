# upload wormhole Dockerfile to r2 
VERSION=1

cd ..
rm -rf out/wormhole
mkdir -p out/wormhole

VERSION=$VERSION docker buildx bake -f rootfs/docker-bake.hcl wormhole
docker save wormhole:$VERSION -o out/wormhole/wormhole.tar

cd out/wormhole && tar -xf wormhole.tar
manifest="$(jq -r '.manifests[0].digest | split(":")[1]' index.json)"

# note: first upload blobs, then manifest, then delete old blobs last
old_blobs="$(aws s3 ls s3://wormhole/blobs/ | awk '{print $4}')"
for layer in "blobs/sha256"/*; do
    hash="${layer##*/}"
    aws s3 cp $layer s3://wormhole/blobs/sha256:$hash --content-type application/vnd.oci.image.layer.v1.tar
done
aws s3 cp blobs/sha256/$manifest s3://wormhole/manifest.json --content-type application/vnd.oci.image.manifest.v1+json
for blob in $old_blobs; do
    aws s3 rm s3://wormhole/$blob
done
