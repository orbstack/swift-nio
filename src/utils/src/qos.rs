use std::io;

use libc::{pthread_set_qos_class_self_np, qos_class_t};

pub enum QosClass {
    UserInteractive,
    UserInitiated,
    Default,
    Utility,
    Background,
}

pub fn set_thread_qos(class: QosClass, priority: Option<i32>) -> io::Result<()> {
    let cls = match class {
        QosClass::UserInteractive => qos_class_t::QOS_CLASS_USER_INTERACTIVE,
        QosClass::UserInitiated => qos_class_t::QOS_CLASS_USER_INITIATED,
        QosClass::Default => qos_class_t::QOS_CLASS_DEFAULT,
        QosClass::Utility => qos_class_t::QOS_CLASS_UTILITY,
        QosClass::Background => qos_class_t::QOS_CLASS_BACKGROUND,
    };

    let ret = unsafe { pthread_set_qos_class_self_np(cls, priority.unwrap_or(0)) };
    if ret != 0 {
        return Err(io::Error::from_raw_os_error(ret));
    }

    Ok(())
}
