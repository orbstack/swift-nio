use std::{
    array, fmt, hash, iter,
    ops::{Index, IndexMut},
    slice,
};

// === NumEnumArray === //

pub trait NumEnumArray:
    Sized + AsMut<[Self::Elem]> + AsRef<[Self::Elem]> + IntoIterator<Item = Self::Elem>
{
    type Elem;
    const LEN: usize;

    fn from_fn(f: impl FnMut(usize) -> Self::Elem) -> Self;

    fn from_iter(iter: impl IntoIterator<Item = Self::Elem>) -> Self;
}

impl<T, const N: usize> NumEnumArray for [T; N] {
    type Elem = T;

    const LEN: usize = N;

    fn from_fn(f: impl FnMut(usize) -> Self::Elem) -> Self {
        array::from_fn(f)
    }

    fn from_iter(iter: impl IntoIterator<Item = Self::Elem>) -> Self {
        let mut iter = iter.into_iter();
        let arr = Self::from_fn(|_| iter.next().unwrap());
        assert!(iter.next().is_none());
        arr
    }
}

// === NumEnum === //

pub type NumEnumVariantIter<T> = iter::Copied<slice::Iter<'static, T>>;

pub trait NumEnum: 'static + fmt::Debug + Copy + hash::Hash + Eq + Ord {
    const COUNT: usize = Self::VARIANTS.len();
    const VARIANTS: &'static [Self];

    type Array<T>: NumEnumArray<Elem = T>;

    fn as_usize(self) -> usize;

    fn try_from_index(index: usize) -> Option<Self> {
        Self::VARIANTS.get(index).copied()
    }

    fn variants() -> NumEnumVariantIter<Self> {
        Self::VARIANTS.iter().copied()
    }
}

#[doc(hidden)]
pub mod define_num_enum_internal {
    pub use {super::NumEnum, std::primitive::usize};
}

#[macro_export]
macro_rules! define_num_enum {
	($(
		$(#[$attr_meta:meta])*
		$vis:vis enum $name:ident {
			$(
				$(#[$field_meta:meta])*
				$field:ident
			),*
			$(,)?
		}
	)*) => {$(
		$(#[$attr_meta])*
		#[derive(Debug, Copy, Clone, Hash, Eq, PartialEq, Ord, PartialOrd)]
		$vis enum $name {
			$(
				$(#[$field_meta])*
				$field
			),*
		}

		impl $crate::num_enum::define_num_enum_internal::NumEnum for $name {
			const VARIANTS: &'static [Self] = &[
				$(Self::$field),*
			];

            type Array<T> = [T; Self::COUNT];

			fn as_usize(self) -> $crate::num_enum::define_num_enum_internal::usize {
				self as $crate::num_enum::define_num_enum_internal::usize
			}
		}
	)*};
}

pub use define_num_enum;

// === NumEnumMap === //

pub type NumEnumMapIter<'a, K, V> = iter::Zip<NumEnumVariantIter<K>, slice::Iter<'a, V>>;

pub type NumEnumMapIterMut<'a, K, V> = iter::Zip<NumEnumVariantIter<K>, slice::IterMut<'a, V>>;

pub type NumEnumMapIntoIter<K, V> =
    iter::Zip<NumEnumVariantIter<K>, <<K as NumEnum>::Array<V> as IntoIterator>::IntoIter>;

pub struct NumEnumMap<K: NumEnum, V>(pub K::Array<V>);

impl<K: NumEnum, V: Default> Default for NumEnumMap<K, V> {
    fn default() -> Self {
        Self(K::Array::from_fn(|_| V::default()))
    }
}

impl<K: NumEnum, V> Clone for NumEnumMap<K, V>
where
    K::Array<V>: Clone,
{
    fn clone(&self) -> Self {
        Self(self.0.clone())
    }
}

impl<K: NumEnum, V> Copy for NumEnumMap<K, V> where K::Array<V>: Copy {}

impl<K: NumEnum, V: fmt::Debug> fmt::Debug for NumEnumMap<K, V> {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_map().entries(self.iter()).finish()
    }
}

impl<K: NumEnum, V> NumEnumMap<K, V> {
    pub fn keys(&self) -> NumEnumVariantIter<K> {
        K::variants()
    }

    pub fn values(&self) -> slice::Iter<'_, V> {
        self.0.as_ref().iter()
    }

    pub fn values_mut(&mut self) -> slice::IterMut<'_, V> {
        self.0.as_mut().iter_mut()
    }

    pub fn iter(&self) -> NumEnumMapIter<'_, K, V> {
        self.keys().zip(self.values())
    }

    pub fn iter_mut(&mut self) -> NumEnumMapIterMut<'_, K, V> {
        self.keys().zip(self.values_mut())
    }

    pub fn into_values(self) -> <K::Array<V> as IntoIterator>::IntoIter {
        self.0.into_iter()
    }
}

impl<K: NumEnum, V> FromIterator<V> for NumEnumMap<K, V> {
    fn from_iter<T: IntoIterator<Item = V>>(iter: T) -> Self {
        Self(K::Array::from_iter(iter))
    }
}

impl<K: NumEnum, V> IntoIterator for NumEnumMap<K, V> {
    type Item = (K, V);
    type IntoIter = NumEnumMapIntoIter<K, V>;

    fn into_iter(self) -> Self::IntoIter {
        self.keys().zip(self.0)
    }
}

impl<'a, K: NumEnum, V> IntoIterator for &'a NumEnumMap<K, V> {
    type Item = (K, &'a V);
    type IntoIter = NumEnumMapIter<'a, K, V>;

    fn into_iter(self) -> Self::IntoIter {
        self.iter()
    }
}

impl<'a, K: NumEnum, V> IntoIterator for &'a mut NumEnumMap<K, V> {
    type Item = (K, &'a mut V);
    type IntoIter = NumEnumMapIterMut<'a, K, V>;

    fn into_iter(self) -> Self::IntoIter {
        self.iter_mut()
    }
}

impl<K: NumEnum, V> Index<K> for NumEnumMap<K, V> {
    type Output = V;

    fn index(&self, index: K) -> &Self::Output {
        &self.0.as_ref()[index.as_usize()]
    }
}

impl<K: NumEnum, V> IndexMut<K> for NumEnumMap<K, V> {
    fn index_mut(&mut self, index: K) -> &mut Self::Output {
        &mut self.0.as_mut()[index.as_usize()]
    }
}
