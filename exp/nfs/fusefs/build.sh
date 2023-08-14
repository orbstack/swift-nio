clang++ -Wall passthrough_llph.cc $(pkg-config fuse3 --cflags --libs)  -o passthrough_llph -Og -fsanitize=address
