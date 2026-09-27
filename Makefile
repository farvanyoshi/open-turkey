# =============================================================================
# Open Turkey - Makefile
# Productivity blocker for Linux
# =============================================================================

# Name of the final binary (compiled by Go)
BINARY_NAME=open-turkey

# Name of the binary installed on the system
# We rename it to "open-turkey-bin" to separate the real binary
# from the wrapper script the user runs directly
BINARY_INSTALLED=open-turkey-bin

# Binary install path
INSTALL_PATH=/usr/local/bin

# Path of the sudoers file that allows running without a password
# We use /etc/sudoers.d/ (a "drop-in" directory) instead of editing
# /etc/sudoers directly — that way the rule is isolated and easy to remove
SUDOERS_FILE=/etc/sudoers.d/open-turkey

# Path to install the systemd service file
SERVICE_PATH=/etc/systemd/system

# Directory to store the SQLite database
DB_DIR=/var/lib/open-turkey

# Include the Go installed at /usr/local/go/bin in PATH
export PATH := /usr/local/go/bin:$(PATH)

# -------------------------------------------------------
# build: compiles the binary
# Uses CGO_ENABLED=0 because we use modernc.org/sqlite
# (a pure Go implementation, with no CGo dependency)
# -------------------------------------------------------
.PHONY: build
build:
	@echo ">> Compiling $(BINARY_NAME)..."
	CGO_ENABLED=0 go build -o $(BINARY_NAME) ./cmd/open-turkey

# -------------------------------------------------------
# install: compiles, copies the binary, installs the systemd
#          service, creates the database directory,
#          configures sudoers and creates the wrapper script
#
# After installation, the user can run:
#   open-turkey start ...
# without having to type "sudo" or a password.
# -------------------------------------------------------
.PHONY: install
install: build
	@echo ">> Installing the real binary as $(BINARY_INSTALLED)..."
	# The real binary gets a different name so that the wrapper
	# script can take the "open-turkey" name
	# We copy to a temporary file and do an atomic rename.
	# This avoids the "Text file busy" (ETXTBSY) error when the daemon
	# is running the current binary.
	tmp="$(INSTALL_PATH)/.$(BINARY_INSTALLED).tmp.$$"; \
	cp $(BINARY_NAME) "$$tmp"; \
	chmod 755 "$$tmp"; \
	mv -f "$$tmp" $(INSTALL_PATH)/$(BINARY_INSTALLED)

	@echo ">> Creating wrapper script at $(INSTALL_PATH)/$(BINARY_NAME)..."
	# The wrapper script is what the user runs when typing "open-turkey".
	# It calls "sudo open-turkey-bin" under the hood.
	# "exec" replaces the shell process with sudo, avoiding an
	# unnecessary intermediate process in the process tree.
	# "$$@" passes through all of the user's arguments (e.g. start, block, etc.)
	printf '#!/bin/bash\n\
# Open Turkey wrapper script\n\
# Calls the real binary (open-turkey-bin) with sudo, transparently.\n\
# The rule in /etc/sudoers.d/open-turkey allows this without a password.\n\
# "exec" replaces this process with sudo — no leftover shell.\n\
exec sudo $(INSTALL_PATH)/$(BINARY_INSTALLED) "$$@"\n' > $(INSTALL_PATH)/$(BINARY_NAME)
	chmod 755 $(INSTALL_PATH)/$(BINARY_NAME)

	@echo ">> Configuring sudoers for passwordless execution..."
	# sudoers rule: allows ANY user to run open-turkey-bin
	# as root, without typing a password.
	#   ALL ALL=(root) NOPASSWD: /usr/local/bin/open-turkey-bin
	#    |   |    |       |
	#    |   |    |       +-- doesn't ask for a password
	#    |   |    +---------- runs as root
	#    |   +--------------- any user
	#    +------------------- on any host
	#
	# chmod 440: only root and the root group can read the file.
	# This is mandatory — sudo refuses files with open permissions.
	printf 'ALL ALL=(root) NOPASSWD: $(INSTALL_PATH)/$(BINARY_INSTALLED)\n' > $(SUDOERS_FILE)
	chmod 440 $(SUDOERS_FILE)

	@echo ">> Installing the systemd service..."
	cp systemd/open-turkey.service $(SERVICE_PATH)/open-turkey.service
	chmod 644 $(SERVICE_PATH)/open-turkey.service

	@echo ">> Creating database directory at $(DB_DIR)..."
	mkdir -p $(DB_DIR)

	@echo ">> Enabling and starting the service..."
	systemctl daemon-reload
	systemctl enable open-turkey.service
	systemctl start open-turkey.service

	@echo ">> Installation completed successfully!"

# -------------------------------------------------------
# uninstall: stops the service, disables it, removes the binary
#            and the systemd service file
# -------------------------------------------------------
.PHONY: uninstall
uninstall:
	@echo ">> Stopping the service..."
	-systemctl stop open-turkey.service

	@echo ">> Disabling the service..."
	-systemctl disable open-turkey.service

	@echo ">> Removing the service file..."
	rm -f $(SERVICE_PATH)/open-turkey.service
	systemctl daemon-reload

	@echo ">> Removing the wrapper script and the real binary..."
	rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	rm -f $(INSTALL_PATH)/$(BINARY_INSTALLED)

	@echo ">> Removing sudoers rule..."
	rm -f $(SUDOERS_FILE)

	@echo ">> Uninstallation completed successfully!"

# -------------------------------------------------------
# clean: removes build artifacts
# -------------------------------------------------------
.PHONY: clean
clean:
	@echo ">> Cleaning build artifacts..."
	rm -f $(BINARY_NAME)
	@echo ">> Cleanup complete!"
