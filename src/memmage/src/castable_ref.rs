#![allow(clippy::missing_safety_doc)]

use std::{mem, ops::Deref, rc::Rc, sync::Arc};

pub unsafe trait CastableRef<'p>: Sized + Deref {
    type WithPointee<V: ?Sized>: CastableRef<'p, Target = V>
    where
        V: 'p;

    unsafe fn from_raw(raw: *const Self::Target) -> Self;

    fn into_raw(me: Self) -> *const Self::Target;

    fn map<V: ?Sized>(self, convert: impl FnOnce(&Self::Target) -> &V) -> Self::WithPointee<V> {
        let original = &*self;
        let converted = convert(original);

        assert_eq!(
            original as *const Self::Target as *const (),
            converted as *const V as *const ()
        );
        assert_eq!(mem::size_of_val(original), mem::size_of_val(converted));

        let converted = converted as *const V;

        let _ = CastableRef::into_raw(self);
        unsafe { CastableRef::from_raw(converted) }
    }
}

unsafe impl<'p, T: ?Sized> CastableRef<'p> for &'p T {
    type WithPointee<V: ?Sized> = &'p V where V: 'p;

    unsafe fn from_raw(raw: *const Self::Target) -> Self {
        &*raw
    }

    fn into_raw(me: Self) -> *const Self::Target {
        me
    }
}

unsafe impl<'p, T: ?Sized> CastableRef<'p> for Rc<T> {
    type WithPointee<V: ?Sized> = Rc<V> where V: 'p;

    unsafe fn from_raw(raw: *const Self::Target) -> Self {
        Rc::from_raw(raw)
    }

    fn into_raw(me: Self) -> *const Self::Target {
        Rc::into_raw(me)
    }
}

unsafe impl<'p, T: ?Sized> CastableRef<'p> for Arc<T> {
    type WithPointee<V: ?Sized> = Arc<V> where V: 'p;

    unsafe fn from_raw(raw: *const Self::Target) -> Self {
        Arc::from_raw(raw)
    }

    fn into_raw(me: Self) -> *const Self::Target {
        Arc::into_raw(me)
    }
}
