# Update signing

To allow for key upgrades, Sparkle requires that either the Apple code signature or the Ed25519 signature is valid. The code signature is verified using the designated requirement:

```
❯ codesign -d -r - /Applications/OrbStack.app
Executable=/Applications/OrbStack.app/Contents/MacOS/OrbStack
designated => anchor apple generic and identifier "dev.kdrag0n.MacVirt" and (certificate leaf[field.1.2.840.113635.100.6.1.9] /* exists */ or certificate 1[field.1.2.840.113635.100.6.2.6] /* exists */ and certificate leaf[field.1.2.840.113635.100.6.1.13] /* exists */ and certificate leaf[subject.OU] = HUAQ24HBR6)
```

... so regenerating the Developer ID Application certificate is OK, and it doesn't need to be backed up. (The Sparkle key is backed up.)
