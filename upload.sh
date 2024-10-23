docker build --ssh default -t localhost:5000/wormhole-rootfs -f wormhole/remote/Dockerfile . 

mkdir -p out/wormhole
echo "exporting docker image"
docker save localhost:5000/wormhole-rootfs:latest -o out/wormhole/wormhole-rootfs.tar

cd out/wormhole
tar -xf wormhole-rootfs.tar


for layer in "blobs/sha256"/*; do
    echo "uploading layer $layer"
    aws s3 cp $layer s3://wormhole/$layer
done

for metadata in "manifest.json" "index.json"; do
    echo "uploading $metadata"
    aws s3 cp $metadata s3://wormhole/$metadata
done

