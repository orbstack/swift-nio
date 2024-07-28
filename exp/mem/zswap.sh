echo zsmalloc > /sys/module/zswap/parameters/zpool
echo lz4 > /sys/module/zswap/parameters/compressor
echo 90 > /sys/module/zswap/parameters/accept_threshold_percent
# % used by compressed, not by uncomp! so for 3x ratio this is 1/3
echo 30 > /sys/module/zswap/parameters/max_pool_percent
echo 1 > /sys/module/zswap/parameters/shrinker_enabled
echo 1 > /sys/module/zswap/parameters/enabled
swapoff /dev/zram0
