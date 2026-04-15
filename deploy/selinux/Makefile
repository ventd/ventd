# Convenience wrapper around the system SELinux policy build tooling.
#
# Build:
#   make           # produces ventd.pp
#
# Install:
#   sudo semodule -i ventd.pp
#   sudo restorecon -Rv /usr/local/bin/ventd /etc/ventd /run/ventd
#
# Uninstall:
#   sudo semodule -r ventd
#
# Required packages:
#   Fedora/RHEL/CentOS:  selinux-policy-devel
#   openSUSE:            selinux-policy-devel
#   Debian/Ubuntu:       selinux-policy-dev

MODULE := ventd

.PHONY: all clean install uninstall

all: $(MODULE).pp

$(MODULE).pp: $(MODULE).te $(MODULE).fc
	$(MAKE) -f /usr/share/selinux/devel/Makefile $@

install: $(MODULE).pp
	semodule -i $<
	restorecon -Rv /usr/local/bin/ventd /etc/ventd /run/ventd

uninstall:
	semodule -r $(MODULE)

clean:
	rm -f $(MODULE).pp $(MODULE).pp.tmp
	rm -rf tmp
