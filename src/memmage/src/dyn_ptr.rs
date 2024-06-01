use std::ops::{Deref, DerefMut};

// === Helpers === //

type DynSmartPtrBox<'p, T> = smallbox::SmallBox<
    dyn 'p + DynSmartPtrInner<'p, Pointee = T>,
    *const dyn DynSmartPtrInner<'p, Pointee = T>,
>;

trait DynSmartPtrInner<'p> {
    type Pointee: ?Sized;

    fn deref(&self) -> &Self::Pointee;

    fn deref_mut(&mut self) -> &mut Self::Pointee;

    fn clone_boxed(&self) -> DynSmartPtrBox<'p, Self::Pointee>;
}

// === DynRef === //

// Public
pub struct DynRef<'p, T: ?Sized>(DynSmartPtrBox<'p, T>);

impl<'p, T: ?Sized> DynRef<'p, T> {
    pub fn new(ptr: impl 'p + Deref<Target = T>) -> Self {
        Self(smallbox::smallbox!(DynRefInner(ptr)))
    }
}

impl<'p, T: ?Sized> Deref for DynRef<'p, T> {
    type Target = T;

    fn deref(&self) -> &Self::Target {
        DynSmartPtrInner::deref(&*self.0)
    }
}

// Inner
struct DynRefInner<T>(T);

impl<'p, T: Deref> DynSmartPtrInner<'p> for DynRefInner<T> {
    type Pointee = T::Target;

    fn deref(&self) -> &Self::Pointee {
        &self.0
    }

    fn deref_mut(&mut self) -> &mut Self::Pointee {
        unreachable!()
    }

    fn clone_boxed(&self) -> DynSmartPtrBox<'p, Self::Pointee> {
        unreachable!()
    }
}

// === CloneDynRef === //

// Public
pub struct CloneDynRef<'p, T: ?Sized>(DynSmartPtrBox<'p, T>);

impl<'p, T: ?Sized> CloneDynRef<'p, T> {
    pub fn new(ptr: impl 'p + Clone + Deref<Target = T>) -> Self {
        Self(smallbox::smallbox!(CloneDynRefInner(ptr)))
    }
}

impl<'p, T: ?Sized> Clone for CloneDynRef<'p, T> {
    fn clone(&self) -> Self {
        Self(self.0.clone_boxed())
    }
}

impl<'p, T: ?Sized> Deref for CloneDynRef<'p, T> {
    type Target = T;

    fn deref(&self) -> &Self::Target {
        DynSmartPtrInner::deref(&*self.0)
    }
}

// Inner
#[derive(Clone)]
struct CloneDynRefInner<T>(T);

impl<'p, T: 'p + Clone + Deref> DynSmartPtrInner<'p> for CloneDynRefInner<T> {
    type Pointee = T::Target;

    fn deref(&self) -> &Self::Pointee {
        &self.0
    }

    fn deref_mut(&mut self) -> &mut Self::Pointee {
        unreachable!()
    }

    fn clone_boxed(&self) -> DynSmartPtrBox<'p, Self::Pointee> {
        smallbox::smallbox!(self.clone())
    }
}

// === DynMut === //

// Public
pub struct DynMut<'p, T: ?Sized>(DynSmartPtrBox<'p, T>);

impl<'p, T: ?Sized> DynMut<'p, T> {
    pub fn new(ptr: impl 'p + DerefMut<Target = T>) -> Self {
        Self(smallbox::smallbox!(DynMutInner(ptr)))
    }
}

impl<'p, T: ?Sized> Deref for DynMut<'p, T> {
    type Target = T;

    fn deref(&self) -> &Self::Target {
        DynSmartPtrInner::deref(&*self.0)
    }
}

impl<'p, T: ?Sized> DerefMut for DynMut<'p, T> {
    fn deref_mut(&mut self) -> &mut Self::Target {
        DynSmartPtrInner::deref_mut(&mut *self.0)
    }
}

// Inner
struct DynMutInner<T>(T);

impl<'p, T: 'p + DerefMut> DynSmartPtrInner<'p> for DynMutInner<T> {
    type Pointee = T::Target;

    fn deref(&self) -> &Self::Pointee {
        &self.0
    }

    fn deref_mut(&mut self) -> &mut Self::Pointee {
        &mut self.0
    }

    fn clone_boxed(&self) -> DynSmartPtrBox<'p, Self::Pointee> {
        unimplemented!()
    }
}
