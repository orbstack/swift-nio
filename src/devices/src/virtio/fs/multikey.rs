// Copyright 2019 The Chromium OS Authors. All rights reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

use std::{
    borrow::Borrow,
    hash::{BuildHasherDefault, Hash},
};

use dashmap::mapref::one::{Ref, RefMut};
use rustc_hash::FxHasher;

use super::FxDashMap;

/// A FxHashMap that supports 2 types of keys per value. All the usual restrictions and warnings for
/// `std::collections::FxHashMap` also apply to this struct. Additionally, there is a 1:1
/// relationship between the 2 key types. In other words, for each `K1` in the map, there is exactly
/// one `K2` in the map and vice versa.
#[derive(Default)]
pub struct MultikeyFxDashMap<K1, K2, V>
where
    K1: Ord + Hash,
    K2: Ord + Hash,
{
    // We need to keep a copy of the second key in the main map so that we can remove entries using
    // just the main key. Otherwise we would require the caller to provide both keys when calling
    // `remove`.
    main: FxDashMap<K1, V>,
    alt: FxDashMap<K2, K1>,
}

type S = BuildHasherDefault<FxHasher>;

impl<K1, K2, V> MultikeyFxDashMap<K1, K2, V>
where
    K1: Clone + Ord + Hash,
    K2: Clone + Ord + Hash,
{
    /// Create a new empty MultikeyFxHashMap.
    pub fn new() -> Self {
        MultikeyFxDashMap {
            main: FxDashMap::default(),
            alt: FxDashMap::default(),
        }
    }

    /// Returns a reference to the value corresponding to the key.
    ///
    /// The key may be any borrowed form of `K1``, but the ordering on the borrowed form must match
    /// the ordering on `K1`.
    pub fn get<Q>(&self, key: &Q) -> Option<Ref<K1, V, S>>
    where
        K1: Borrow<Q>,
        Q: Ord + ?Sized + Hash,
    {
        self.main.get(key)
    }

    pub fn get_mut<Q>(&self, key: &Q) -> Option<RefMut<K1, V, S>>
    where
        K1: Borrow<Q>,
        Q: Ord + ?Sized + Hash,
    {
        self.main.get_mut(key)
    }

    /// Returns a reference to the value corresponding to the alternate key.
    ///
    /// The key may be any borrowed form of the `K2``, but the ordering on the borrowed form must
    /// match the ordering on `K2`.
    ///
    /// Note that this method performs 2 lookups: one to get the main key and another to get the
    /// value associated with that key. For best performance callers should prefer the `get` method
    /// over this method whenever possible as `get` only needs to perform one lookup.
    pub fn get_alt<Q2>(&self, key: &Q2) -> Option<Ref<K1, V, S>>
    where
        K2: Borrow<Q2>,
        Q2: Ord + ?Sized + Hash,
    {
        if let Some(k) = self.alt.get(key) {
            self.get(&k)
        } else {
            None
        }
    }

    pub fn get_alt_mut<Q2>(&self, key: &Q2) -> Option<RefMut<K1, V, S>>
    where
        K2: Borrow<Q2>,
        Q2: Ord + ?Sized + Hash,
    {
        if let Some(k) = self.alt.get(key) {
            self.get_mut(&k)
        } else {
            None
        }
    }

    /// Inserts a new entry into the map with the given keys and value.
    pub fn insert(&self, k1: K1, k2: K2, v: V) -> K1 {
        // always add K1 first to prevent race. reverse mapping requires original to exist already
        self.main.insert(k1.clone(), v);
        // add or replace K2->K1 mapping to new K1 value
        self.insert_alt(k1, k2)
    }

    pub fn insert_alt(&self, k1: K1, k2: K2) -> K1 {
        self.alt.entry(k2).or_insert(k1).clone()
    }

    // dual-remove can be racy, so user must deal with the precise k1/k2 removal semantics
    pub fn remove_main<Q>(&self, key: &Q)
    where
        K1: Borrow<Q>,
        Q: Ord + ?Sized + Hash,
    {
        self.main.remove(key);
    }

    pub fn remove_alt(&self, k2: &K2) {
        self.alt.remove(k2);
    }

    pub fn contains_alt_key(&self, key: &K2) -> bool {
        self.alt.contains_key(key)
    }

    pub fn entry(&self, key: K1) -> dashmap::mapref::entry::Entry<K1, V, S> {
        self.main.entry(key)
    }

    pub fn iter_main(&self) -> dashmap::iter::Iter<K1, V, S> {
        self.main.iter()
    }

    /// Clears the map, removing all values.
    pub fn clear(&self) {
        self.alt.clear();
        self.main.clear()
    }

    pub fn len(&self) -> usize {
        self.main.len()
    }
}

/*
#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn get() {
        let mut m = MultikeyFxDashMap::<u64, i64, u32>::new();

        let k1 = 0xc6c8_f5e0_b13e_ed40;
        let k2 = 0x1a04_ce4b_8329_14fe;
        let val = 0xf4e3_c360;
        assert!(m.insert(k1, k2, val).is_none());

        assert_eq!(*m.get(&k1).expect("failed to look up main key"), val);
        assert_eq!(*m.get_alt(&k2).expect("failed to look up alt key"), val);
    }

    #[test]
    fn update_main_key() {
        let mut m = MultikeyFxDashMap::<u64, i64, u32>::new();

        let k1 = 0xc6c8_f5e0_b13e_ed40;
        let k2 = 0x1a04_ce4b_8329_14fe;
        let val = 0xf4e3_c360;
        assert!(m.insert(k1, k2, val).is_none());

        let new_k1 = 0x3add_f8f8_c7c5_df5e;
        let val2 = 0x7389_f8a7;
        assert_eq!(
            m.insert(new_k1, k2, val2)
                .expect("failed to update main key"),
            val
        );

        assert!(m.get(&k1).is_none());
        assert_eq!(*m.get(&new_k1).expect("failed to look up main key"), val2);
        assert_eq!(*m.get_alt(&k2).expect("failed to look up alt key"), val2);
    }

    #[test]
    fn update_alt_key() {
        let mut m = MultikeyFxDashMap::<u64, i64, u32>::new();

        let k1 = 0xc6c8_f5e0_b13e_ed40;
        let k2 = 0x1a04_ce4b_8329_14fe;
        let val = 0xf4e3_c360;
        assert!(m.insert(k1, k2, val).is_none());

        let new_k2 = 0x6825_a60b_61ac_b333;
        let val2 = 0xbb14_8f2c;
        assert_eq!(
            m.insert(k1, new_k2, val2)
                .expect("failed to update alt key"),
            val
        );

        assert!(m.get_alt(&k2).is_none());
        assert_eq!(*m.get(&k1).expect("failed to look up main key"), val2);
        assert_eq!(
            *m.get_alt(&new_k2).expect("failed to look up alt key"),
            val2
        );
    }

    #[test]
    fn update_value() {
        let mut m = MultikeyFxDashMap::<u64, i64, u32>::new();

        let k1 = 0xc6c8_f5e0_b13e_ed40;
        let k2 = 0x1a04_ce4b_8329_14fe;
        let val = 0xf4e3_c360;
        assert!(m.insert(k1, k2, val).is_none());

        let val2 = 0xe42d_79ba;
        assert_eq!(
            m.insert(k1, k2, val2).expect("failed to update alt key"),
            val
        );

        assert_eq!(*m.get(&k1).expect("failed to look up main key"), val2);
        assert_eq!(*m.get_alt(&k2).expect("failed to look up alt key"), val2);
    }

    #[test]
    fn update_both_keys_main() {
        let mut m = MultikeyFxDashMap::<u64, i64, u32>::new();

        let k1 = 0xc6c8_f5e0_b13e_ed40;
        let k2 = 0x1a04_ce4b_8329_14fe;
        let val = 0xf4e3_c360;
        assert!(m.insert(k1, k2, val).is_none());

        let new_k1 = 0xc980_587a_24b3_ae30;
        let new_k2 = 0x2773_c5ee_8239_45a2;
        let val2 = 0x31f4_33f9;
        assert!(m.insert(new_k1, new_k2, val2).is_none());

        let val3 = 0x8da1_9cf7;
        assert_eq!(
            m.insert(k1, new_k2, val3)
                .expect("failed to update main key"),
            val
        );

        // Both new_k1 and k2 should now be gone from the map.
        assert!(m.get(&new_k1).is_none());
        assert!(m.get_alt(&k2).is_none());

        assert_eq!(*m.get(&k1).expect("failed to look up main key"), val3);
        assert_eq!(
            *m.get_alt(&new_k2).expect("failed to look up alt key"),
            val3
        );
    }

    #[test]
    fn update_both_keys_alt() {
        let mut m = MultikeyFxDashMap::<u64, i64, u32>::new();

        let k1 = 0xc6c8_f5e0_b13e_ed40;
        let k2 = 0x1a04_ce4b_8329_14fe;
        let val = 0xf4e3_c360;
        assert!(m.insert(k1, k2, val).is_none());

        let new_k1 = 0xc980_587a_24b3_ae30;
        let new_k2 = 0x2773_c5ee_8239_45a2;
        let val2 = 0x31f4_33f9;
        assert!(m.insert(new_k1, new_k2, val2).is_none());

        let val3 = 0x8da1_9cf7;
        assert_eq!(
            m.insert(new_k1, k2, val3)
                .expect("failed to update main key"),
            val2
        );

        // Both k1 and new_k2 should now be gone from the map.
        assert!(m.get(&k1).is_none());
        assert!(m.get_alt(&new_k2).is_none());

        assert_eq!(*m.get(&new_k1).expect("failed to look up main key"), val3);
        assert_eq!(*m.get_alt(&k2).expect("failed to look up alt key"), val3);
    }

    #[test]
    fn remove() {
        let mut m = MultikeyFxDashMap::<u64, i64, u32>::new();

        let k1 = 0xc6c8_f5e0_b13e_ed40;
        let k2 = 0x1a04_ce4b_8329_14fe;
        let val = 0xf4e3_c360;
        assert!(m.insert(k1, k2, val).is_none());

        assert_eq!(m.remove(&k1).expect("failed to remove entry"), val);
        assert!(m.get(&k1).is_none());
        assert!(m.get_alt(&k2).is_none());
    }
}
*/
