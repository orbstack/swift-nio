docker build --ssh default -t localhost:5000/wormhole-rootfs -f wormhole/remote/Dockerfile-server . 

rm -rf out/wormhole
mkdir -p out/wormhole
echo "exporting docker image"
#docker save localhost:5000/wormhole-rootfs:latest -o out/wormhole/wormhole-rootfs.tar
docker save alpine:latest -o out/wormhole/wormhole-rootfs.tar

cd out/wormhole
tar -xf wormhole-rootfs.tar

# make manifest.json
python3 ../../make_manifest.py index.json manifest.json


aws s3 rm s3://wormhole/ --recursive

for metadata in "oci.image.manifest.json"; do
    echo "uploading $metadata"
    aws s3 cp $metadata s3://wormhole/$metadata --content-type  application/vnd.oci.image.manifest.v1+json
done

for layer in "blobs/sha256"/*; do
    hash="${layer##*/}"
    echo "uploading layer $hash"
    aws s3 cp $layer s3://wormhole/blobs/sha256:$hash --content-type application/vnd.oci.image.layer.v1.tar
done

