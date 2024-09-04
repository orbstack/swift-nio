use criterion::{criterion_group, criterion_main, Criterion};
use libkrun_asm_tests::*;

fn entry(c: &mut Criterion) {
    c.bench_function("index_slice_sized_runtime_1", |c| {
        let slice = (0..1024).map(|_| 0u8).collect::<Box<_>>();
        c.iter(|| index_slice_sized_runtime_1(&slice, 10, 20))
    });

    c.bench_function("index_slice_sized_runtime_2", |c| {
        let slice = (0..1024).map(|_| 0u8).collect::<Box<_>>();
        c.iter(|| index_slice_sized_runtime_2(&slice, 10, 20))
    });
}

criterion_group!(group, entry);
criterion_main!(group);
