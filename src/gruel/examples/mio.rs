use std::sync::{Arc, Mutex};

use gruel::{
    multiplex_signals_with_shutdown, MioDispatcher, MioMultiplexHandler, RawSignalChannel,
    ShutdownSignal, SignalMultiplexHandler,
};
use memmage::CloneDynRef;
use mio::{event::Event, Interest};

fn main() {
    let shutdown = ShutdownSignal::new();

    multiplex_signals_with_shutdown(
        &shutdown,
        &mut [&mut MyHandlerRef(Arc::new(Mutex::new(MyHandler {})))],
        MioDispatcher::new().unwrap(),
    );
}

#[derive(Debug)]
pub struct MyHandler {}

#[derive(Debug, Clone)]
pub struct MyHandlerRef(Arc<Mutex<MyHandler>>);

impl SignalMultiplexHandler<MioDispatcher> for MyHandlerRef {
    fn process(&mut self, cx: &mut MioDispatcher) {
        let me = self.0.lock().unwrap();

        // Do stuff with me
        drop(me);

        cx.register(&mut dummy_source(), Interest::READABLE, self.clone())
            .unwrap();
    }

    fn signals(&self) -> Vec<CloneDynRef<'static, RawSignalChannel>> {
        vec![]
    }
}

impl MioMultiplexHandler for MyHandlerRef {
    fn process(&mut self, _event: &Event) {
        let me = self.0.lock().unwrap();

        // Do stuff with `me`
        drop(me);
    }
}

fn dummy_source() -> Box<dyn mio::event::Source> {
    todo!();
}
