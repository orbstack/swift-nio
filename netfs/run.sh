netatalk

smbpasswd -c smb.conf -D 3  -a root
smbd -s smb.conf -d 3 -i  -F -l /tmp/samba


