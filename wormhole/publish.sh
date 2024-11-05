# upload wormhole Dockerfile to r2 
cd ..
rm -rf out/wormhole
mkdir -p out/wormhole

docker build --ssh default -t wormhole -f wormhole/remote/Dockerfile . 
docker save wormhole:latest -o out/wormhole/wormhole.tar
# docker save alpine:latest -o out/wormhole/wormhole.tar

cd out/wormhole && tar -xf wormhole.tar
manifest=$(jq -r '.manifests[0].digest | split(":")[1]' index.json)

# q: upload to a new version-specific directory and update drmserver to fetch from the latest?
# how do we atomically update the wormhole image
aws s3 rm s3://wormhole/ --recursive

aws s3 cp blobs/sha256/$manifest s3://wormhole/manifest.json --content-type application/vnd.oci.image.manifest.v1+json
for layer in "blobs/sha256"/*; do
    hash="${layer##*/}"
    aws s3 cp $layer s3://wormhole/blobs/sha256:$hash --content-type application/vnd.oci.image.layer.v1.tar
done

