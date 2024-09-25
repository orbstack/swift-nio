use std::io;

use hdrhistogram::Histogram;

pub trait HistogramExt {
    fn dump(&self, w: &mut impl io::Write, label: &str) -> io::Result<()>;
}

impl HistogramExt for Histogram<u64> {
    fn dump(&self, w: &mut impl io::Write, label: &str) -> io::Result<()> {
        writeln!(w, "\n\n-----------")?;
        writeln!(w, "{}:", label)?;
        writeln!(w, "  min = {}", self.min())?;
        writeln!(w, "  max = {}", self.max())?;
        writeln!(w, "  mean = {}", self.mean())?;
        writeln!(w, "  stddev = {}", self.stdev())?;
        writeln!(w)?;
        writeln!(w, "  p50 = {}", self.value_at_quantile(0.5))?;
        writeln!(w, "  p90 = {}", self.value_at_quantile(0.9))?;
        writeln!(w, "  p99 = {}", self.value_at_quantile(0.99))?;
        writeln!(w, "  p99.9 = {}", self.value_at_quantile(0.999))?;
        writeln!(w)?;

        // for v in self.iter_recorded() {
        //     writeln!(w, "  p{} = {}  ({} samples)", v.percentile(), v.value_iterated_to(), v.count_at_value())?;
        // }

        Ok(())
    }
}
