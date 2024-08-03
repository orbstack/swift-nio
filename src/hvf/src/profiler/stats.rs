use hdrhistogram::Histogram;

pub fn dump_histogram(label: &str, hist: &Histogram<u64>) {
    println!("\n\n-----------");
    println!("{}:", label);
    println!("  min = {}", hist.min());
    println!("  max = {}", hist.max());
    println!("  mean = {}", hist.mean());
    println!("  stddev = {}", hist.stdev());
    println!();
    println!("  p50 = {}", hist.value_at_quantile(0.5));
    println!("  p90 = {}", hist.value_at_quantile(0.9));
    println!("  p99 = {}", hist.value_at_quantile(0.99));
    println!("  p99.9 = {}", hist.value_at_quantile(0.999));
    println!();

    // for v in hist.iter_recorded() {
    //     println!("  p{} = {}  ({} samples)", v.percentile(), v.value_iterated_to(), v.count_at_value());
    // }
}
