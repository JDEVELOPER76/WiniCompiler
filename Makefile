.PHONY: build install uninstall test clean

# Instalación estándar estilo Unix:
#   bin/winic + share/wini/{modulos,runtime}
# Por defecto instala en el usuario (~/.local, ya en el PATH vía ~/.profile),
# sin sudo. Para sistema: sudo make install PREFIX=/usr/local
PREFIX ?= $(HOME)/.local
BINDIR = $(PREFIX)/bin
DATADIR = $(PREFIX)/share/wini

APP = .
BIN = winic

build:
	go build -o $(BIN) $(APP)

# Smoke test: compila los ejemplos y un archivo en /tmp sin modulos locales
# (usa la búsqueda global vía WINI_PATH).
test: build
	go vet ./...
	TMPD=$$(mktemp -d) && \
	WINI_PATH="$(CURDIR)/modulos" ./$(BIN) -llvm "$$TMPD/smoke.ll" build/main.wn && \
	printf 'importar "tiempo/tiempo" como time\n\nfuncion principal() -> entero:\n    time.dormir(0.0)\n    escribir("ok")\n    retornar 0\n' > "$$TMPD/hola.wn" && \
	WINI_PATH="$(CURDIR)/modulos" ./$(BIN) -llvm "$$TMPD/hola.ll" "$$TMPD/hola.wn" && \
	rm -rf "$$TMPD" && \
	echo "OK: módulos resuelven sin estar junto al .wn (búsqueda global)"

install: build
	install -d "$(BINDIR)" "$(DATADIR)"
	install -m 755 "$(BIN)" "$(BINDIR)/$(BIN)"
	rm -rf "$(DATADIR)/modulos" "$(DATADIR)/runtime"
	cp -r modulos "$(DATADIR)/modulos"
	cp -r runtime "$(DATADIR)/runtime"
	@echo ""
	@echo "Instalado:"
	@echo "  binario : $(BINDIR)/$(BIN)"
	@echo "  módulos : $(DATADIR)/modulos"
	@echo "  runtime : $(DATADIR)/runtime"
	@echo ""
	@echo "El compilador ya busca en $(DATADIR)/modulos sin necesidad de"
	@echo "WINI_PATH (ubicación estándar). Si querés ser explícito, agregá:"
	@echo ""
	@echo "  export WINI_PATH=\"$(DATADIR)/modulos\""
	@echo ""
	@echo "Y asegurate de tener $(BINDIR) en el PATH (ver scripts/install.sh)."

uninstall:
	rm -f "$(BINDIR)/$(BIN)"
	rm -rf "$(DATADIR)"

clean:
	rm -f $(BIN)
