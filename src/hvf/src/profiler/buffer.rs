use std::iter::FlatMap;

pub struct SegVec<T, const N: usize> {
    segments: Vec<Vec<T>>,
    len: usize,
}

// can't use impl: IntoIterator needs this because opaque types as associated types are unstable
type Iter<'a, T> = FlatMap<
    std::slice::Iter<'a, Vec<T>>,
    std::slice::Iter<'a, T>,
    fn(&'a Vec<T>) -> std::slice::Iter<'a, T>,
>;

type IterMut<'a, T> = FlatMap<
    std::slice::IterMut<'a, Vec<T>>,
    std::slice::IterMut<'a, T>,
    fn(&'a mut Vec<T>) -> std::slice::IterMut<'a, T>,
>;

impl<T, const N: usize> SegVec<T, N> {
    pub fn new() -> Self {
        Self {
            segments: Vec::new(),
            len: 0,
        }
    }

    pub fn push(&mut self, value: T) {
        if self.segments.is_empty() || self.segments.last().unwrap().len() == N {
            self.segments.push(Vec::with_capacity(N));
        }

        self.segments.last_mut().unwrap().push(value);
        self.len += 1;
    }

    pub fn len(&self) -> usize {
        self.len
    }

    pub fn iter(&self) -> Iter<T> {
        self.segments.iter().flat_map(|s| s.iter())
    }

    pub fn iter_mut(&mut self) -> IterMut<T> {
        self.segments.iter_mut().flat_map(|s| s.iter_mut())
    }
}

impl<T, const N: usize> Default for SegVec<T, N> {
    fn default() -> Self {
        Self::new()
    }
}

impl<'a, T, const N: usize> IntoIterator for &'a SegVec<T, N> {
    type Item = &'a T;
    type IntoIter = Iter<'a, T>;

    fn into_iter(self) -> Self::IntoIter {
        self.iter()
    }
}

impl<'a, T, const N: usize> IntoIterator for &'a mut SegVec<T, N> {
    type Item = &'a mut T;
    type IntoIter = IterMut<'a, T>;

    fn into_iter(self) -> Self::IntoIter {
        self.iter_mut()
    }
}
