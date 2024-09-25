#[derive(Debug, Eq, PartialEq)]
pub(crate) enum QueueDirection {
    Rx,
    Tx,
}

#[must_use]
pub(crate) fn port_id_to_queue_idx(queue_direction: QueueDirection, port_id: usize) -> usize {
    match queue_direction {
        QueueDirection::Rx if port_id == 0 => 0,
        QueueDirection::Rx => 2 + 2 * port_id,
        QueueDirection::Tx if port_id == 0 => 1,
        QueueDirection::Tx => 2 + 2 * port_id + 1,
    }
}

pub(crate) fn num_queues(num_ports: usize) -> usize {
    // 2 control queues and then an rx and tx queue for each port
    2 + 2 * num_ports
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn test_port_id_to_queue_idx() {
        assert_eq!(port_id_to_queue_idx(QueueDirection::Rx, 0), 0);
        assert_eq!(port_id_to_queue_idx(QueueDirection::Tx, 0), 1);
        assert_eq!(port_id_to_queue_idx(QueueDirection::Rx, 1), 4);
        assert_eq!(port_id_to_queue_idx(QueueDirection::Tx, 1), 5);
    }
}
