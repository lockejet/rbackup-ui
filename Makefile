# ============================================================
# rbackup-ui Makefile
# ============================================================

BIN        := rbackup-ui
TUI        := rbackup-tui
WEB        := rbackup-web
BUILD_DIR  := bin
DIST_DIR   := dist
STAGE_DIR  := .staging

# ---------- 安装路径 ----------
PREFIX     ?= $(HOME)/.local
BINDIR     ?= $(PREFIX)/bin

# ---------- 打包源文件 ----------
SCRIPT_FILE  := rbackup-cli
CONFIG_FILES := config1.ini.example config2.ini.example
DOC_FILES    := README.md LICENSE

# ---------- 版本 ----------
BUILD_VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
VERSION      ?= $(shell git describe --tags --abbrev=0 2>/dev/null || echo dev)
PKG_VERSION  ?= $(VERSION)
PKG_NAME     := rbackup-ui-$(PKG_VERSION)

LDFLAGS := -X main.Version=$(BUILD_VERSION) \
           -X main.BuildTime=$(shell date +%Y-%m-%d) \
           -X main.GitCommit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none)

# ---------- 平台 ----------
UNAME_S := $(shell uname -s)
ifeq ($(UNAME_S),Linux)
    GOOS   := linux
    SUFFIX :=
else
    GOOS   := windows
    SUFFIX := .exe
endif

TARGET := $(BUILD_DIR)/$(GOOS)/$(TUI)$(SUFFIX)

# ---------- 发布 ----------
REPO ?= $(shell git remote get-url origin 2>/dev/null | head -n1 | \
           sed -E 's|^[^:]+://[^/]+/||; s|^git@[^:]+:||; s|\.git$$||')
NOTES ?= $(DIST_DIR)/RELEASE_NOTES.md
RELEASE_BASE ?= https://github.com/$(REPO)/releases/download/$(VERSION)

# ---------- 安装参数 ----------
SYSTEM     ?= 0                          # SYSTEM=1 → 系统级
SUDO       ?=                            # 系统级安装时置为 sudo
FROM       ?=                            # 离线安装用的本地预编译包
EXPECT_TAG ?=                            # CI: 期望的 tag
# clone 模式下默认安装与当前检出匹配的版本；无 tag 时退化为 latest
PREBUILT_VERSION ?= $(if $(filter dev,$(VERSION)),latest,$(VERSION))

# ============================================================
# 构建
# ============================================================

.PHONY: build
build: require-go
	@mkdir -p $(BUILD_DIR)/$(GOOS)
	go build -ldflags "$(LDFLAGS)" -o $(TARGET) .
	@echo ">>> $(TARGET)"

.PHONY: build-win
build-win:
	@mkdir -p $(BUILD_DIR)/windows
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" \
	    -o $(BUILD_DIR)/windows/$(TUI).exe .

.PHONY: build-linux
build-linux:
	@mkdir -p $(BUILD_DIR)/linux
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" \
	    -o $(BUILD_DIR)/linux/$(TUI) .

.PHONY: build-linux-arm64
build-linux-arm64:
	@mkdir -p $(BUILD_DIR)/linux
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" \
	    -o $(BUILD_DIR)/linux/$(TUI)-arm64 .

.PHONY: build-all
build-all: build-win build-linux build-linux-arm64

# ============================================================
# 打包
# ============================================================

.PHONY: package package-linux package-linux-arm64 package-win
package: package-linux package-linux-arm64 package-win
	@echo ""
	@echo ">>> 打包完成，产物在 $(DIST_DIR)/"
	@ls -lh $(DIST_DIR)/

package-linux: build-linux
	@rm -f $(DIST_DIR)/$(BIN)-*-linux-amd64.tar.gz
	@rm -rf $(STAGE_DIR)/$(BIN)-linux-amd64
	@mkdir -p $(STAGE_DIR)/$(BIN)-linux-amd64 $(DIST_DIR)
	@cp bin/linux/$(TUI) $(STAGE_DIR)/$(BIN)-linux-amd64/
	@ln $(STAGE_DIR)/$(BIN)-linux-amd64/$(TUI) $(STAGE_DIR)/$(BIN)-linux-amd64/$(WEB) 2>/dev/null || \
	    cp $(STAGE_DIR)/$(BIN)-linux-amd64/$(TUI) $(STAGE_DIR)/$(BIN)-linux-amd64/$(WEB)
	@cp $(SCRIPT_FILE) $(STAGE_DIR)/$(BIN)-linux-amd64/
	@cp $(CONFIG_FILES) $(STAGE_DIR)/$(BIN)-linux-amd64/
	@cp $(DOC_FILES) $(STAGE_DIR)/$(BIN)-linux-amd64/
	@mkdir -p $(STAGE_DIR)/$(BIN)-linux-amd64/docs/images
	@cp docs/images/ui-overview.svg $(STAGE_DIR)/$(BIN)-linux-amd64/docs/images/
	@chmod +x $(STAGE_DIR)/$(BIN)-linux-amd64/$(TUI) \
	    $(STAGE_DIR)/$(BIN)-linux-amd64/$(WEB) \
	    $(STAGE_DIR)/$(BIN)-linux-amd64/$(SCRIPT_FILE)
	@tar czf $(DIST_DIR)/$(PKG_NAME)-linux-amd64.tar.gz -C $(STAGE_DIR) $(BIN)-linux-amd64
	@echo ">>> $(DIST_DIR)/$(PKG_NAME)-linux-amd64.tar.gz"

package-linux-arm64: build-linux-arm64
	@rm -f $(DIST_DIR)/$(BIN)-*-linux-arm64.tar.gz
	@rm -rf $(STAGE_DIR)/$(BIN)-linux-arm64
	@mkdir -p $(STAGE_DIR)/$(BIN)-linux-arm64 $(DIST_DIR)
	@cp bin/linux/$(TUI)-arm64 $(STAGE_DIR)/$(BIN)-linux-arm64/$(TUI)
	@ln $(STAGE_DIR)/$(BIN)-linux-arm64/$(TUI) $(STAGE_DIR)/$(BIN)-linux-arm64/$(WEB) 2>/dev/null || \
	    cp $(STAGE_DIR)/$(BIN)-linux-arm64/$(TUI) $(STAGE_DIR)/$(BIN)-linux-arm64/$(WEB)
	@cp $(SCRIPT_FILE) $(STAGE_DIR)/$(BIN)-linux-arm64/
	@cp $(CONFIG_FILES) $(STAGE_DIR)/$(BIN)-linux-arm64/
	@cp $(DOC_FILES) $(STAGE_DIR)/$(BIN)-linux-arm64/
	@mkdir -p $(STAGE_DIR)/$(BIN)-linux-arm64/docs/images
	@cp docs/images/ui-overview.svg $(STAGE_DIR)/$(BIN)-linux-arm64/docs/images/
	@chmod +x $(STAGE_DIR)/$(BIN)-linux-arm64/$(TUI) \
	    $(STAGE_DIR)/$(BIN)-linux-arm64/$(WEB) \
	    $(STAGE_DIR)/$(BIN)-linux-arm64/$(SCRIPT_FILE)
	@tar czf $(DIST_DIR)/$(PKG_NAME)-linux-arm64.tar.gz -C $(STAGE_DIR) $(BIN)-linux-arm64
	@echo ">>> $(DIST_DIR)/$(PKG_NAME)-linux-arm64.tar.gz"

package-win: build-win
	@rm -f $(DIST_DIR)/$(BIN)-*-windows-amd64.zip
	@rm -rf $(STAGE_DIR)/$(BIN)-windows-amd64
	@mkdir -p $(STAGE_DIR)/$(BIN)-windows-amd64 $(DIST_DIR)
	@cp bin/windows/$(TUI).exe $(STAGE_DIR)/$(BIN)-windows-amd64/
	@cp $(STAGE_DIR)/$(BIN)-windows-amd64/$(TUI).exe $(STAGE_DIR)/$(BIN)-windows-amd64/$(WEB).exe
	@cp $(SCRIPT_FILE) $(STAGE_DIR)/$(BIN)-windows-amd64/
	@cp $(CONFIG_FILES) $(STAGE_DIR)/$(BIN)-windows-amd64/
	@cp $(DOC_FILES) $(STAGE_DIR)/$(BIN)-windows-amd64/
	@mkdir -p $(STAGE_DIR)/$(BIN)-windows-amd64/docs/images
	@cp docs/images/ui-overview.svg $(STAGE_DIR)/$(BIN)-windows-amd64/docs/images/
	@[ -f install.ps1 ] && cp install.ps1 $(STAGE_DIR)/$(BIN)-windows-amd64/ || true
	@cd $(STAGE_DIR) && zip -qr ../$(DIST_DIR)/$(PKG_NAME)-windows-amd64.zip $(BIN)-windows-amd64
	@echo ">>> $(DIST_DIR)/$(PKG_NAME)-windows-amd64.zip"

# ============================================================
# 发布（GitHub Release）
# ============================================================

.PHONY: release-check
release-check:
	@test "$(VERSION)" != "dev" || { \
		echo "错误: 无法从 git 解析版本（浅克隆缺少 tag？）"; \
		echo "提示: git fetch --tags"; \
		exit 1; \
	}
	@git rev-parse "$(VERSION)" >/dev/null 2>&1 || { \
		echo "错误: tag '$(VERSION)' 不存在"; \
		echo "先打 tag: git tag -a $(VERSION) -m '...'"; \
		echo "然后推送: git push origin $(VERSION)"; \
		exit 1; \
	}
	@[ -n "$(REPO)" ] || { \
		echo "错误: 无法从 git remote 解析仓库"; \
		echo "请检查: git remote -v"; \
		exit 1; \
	}
	@echo ">>> 仓库: $(REPO)"
	@echo ">>> 版本: $(VERSION)"

.PHONY: release-upload-check
release-upload-check:
	@command -v gh >/dev/null 2>&1 || { \
		echo "错误: 未安装 gh CLI"; \
		echo "安装: https://cli.github.com/"; \
		exit 1; \
	}
	@gh auth status >/dev/null 2>&1 || { \
		echo "错误: gh 未登录"; \
		echo "运行: gh auth login"; \
		exit 1; \
	}
	@echo ">>> gh 就绪"

.PHONY: release-notes
release-notes:
	@mkdir -p $(DIST_DIR)
	@echo ">>> 生成 $(NOTES)"
	@PREV_TAG=$$(git describe --tags --abbrev=0 "$(VERSION)^" 2>/dev/null || echo ""); \
	if [ -z "$$PREV_TAG" ]; then \
		echo "## $(VERSION)" > "$(NOTES)"; \
		echo "" >> "$(NOTES)"; \
		echo "首个发布版本。" >> "$(NOTES)"; \
		echo "" >> "$(NOTES)"; \
		echo "### 提交列表" >> "$(NOTES)"; \
		echo "" >> "$(NOTES)"; \
		git log --pretty="- %s" "$(VERSION)" >> "$(NOTES)"; \
	else \
		echo "## $(VERSION)" > "$(NOTES)"; \
		echo "" >> "$(NOTES)"; \
		echo "自 $$PREV_TAG 以来的改动：" >> "$(NOTES)"; \
		echo "" >> "$(NOTES)"; \
		git log --pretty="- %s" "$$PREV_TAG..$(VERSION)" >> "$(NOTES)"; \
	fi
	@echo ""
	@echo "--- $(NOTES) 内容 ---"
	@cat "$(NOTES)"
	@echo "--- 结束 ---"
	@echo ""

# 把安装器与校验和纳入发布资产
.PHONY: stage-release-extras
stage-release-extras:
	@mkdir -p $(DIST_DIR)
	@install -m 0755 install.sh $(DIST_DIR)/install.sh
	@install -m 0644 install.ps1 $(DIST_DIR)/install.ps1
	@echo ">>> $(DIST_DIR)/install.sh $(DIST_DIR)/install.ps1"

.PHONY: checksums
checksums:
	@cd $(DIST_DIR) && rm -f SHA256SUMS && \
	  for f in *.tar.gz *.zip install.sh install.ps1; do \
	    [ -e "$$f" ] || continue; \
	    sha256sum "$$f" >> SHA256SUMS; \
	  done
	@echo ">>> $(DIST_DIR)/SHA256SUMS"
	@cat $(DIST_DIR)/SHA256SUMS

# CI 用：确保版本解析正确（浅克隆缺 tag 时 PKG_VERSION 会退化成 dev）
.PHONY: check-version
check-version:
	@test "$(VERSION)" != "dev" || { \
		echo "错误: 版本解析为 dev —— 通常是浅克隆缺少 tag"; \
		echo "修复: actions/checkout 需 fetch-depth: 0"; \
		exit 1; \
	}
	@if [ -n "$(EXPECT_TAG)" ]; then \
		test "$(VERSION)" = "$(EXPECT_TAG)" || { \
			echo "错误: tag '$(EXPECT_TAG)' 与解析出的 '$(VERSION)' 不一致"; \
			exit 1; \
		}; \
	fi
	@echo ">>> 版本校验通过: $(VERSION)"

# 校验已发布资产：下载 + 比对 SHA256SUMS
.PHONY: verify-release
verify-release:
	@test "$(VERSION)" != "dev" || { echo "用法: make verify-release VERSION=v1.1.6"; exit 1; }
	@tmp=$$(mktemp -d); trap 'rm -rf "$$tmp"' EXIT; \
	  echo ">>> 从 $(RELEASE_BASE) 下载校验"; \
	  for f in $(PKG_NAME)-linux-amd64.tar.gz $(PKG_NAME)-linux-arm64.tar.gz \
	           $(PKG_NAME)-windows-amd64.zip SHA256SUMS install.sh; do \
	    curl -fsSL -o "$$tmp/$$f" "$(RELEASE_BASE)/$$f" \
	      || { echo "✗ 下载失败: $$f"; exit 1; }; \
	    echo "  ✓ $$f"; \
	  done; \
	  cd "$$tmp" && sha256sum -c SHA256SUMS && \
	  bash -n install.sh && echo ">>> install.sh 语法 OK" && \
	  echo ">>> 全部校验通过"

.PHONY: release
release: release-check package stage-release-extras checksums release-notes
	@echo ""
	@echo ">>> 本地发布包已就绪（未上传任何东西）"
	@ls -lh $(DIST_DIR)/
	@echo ""
	@echo ">>> 正式发布由 GitHub Actions 完成：推送 tag 即可"
	@echo "    git tag -a $(VERSION) -m '$(VERSION)'    # 若尚未打 tag"
	@echo "    git push origin $(VERSION)"
	@echo ""
	@echo ">>> 应急手工上传: make release-upload"

.PHONY: release-upload
release-upload: release-check release-upload-check package stage-release-extras checksums
	@echo ">>> 上传附件到 Release: $(VERSION)"
	@if gh release view "$(VERSION)" --repo "$(REPO)" >/dev/null 2>&1; then \
		gh release upload "$(VERSION)" \
		    $(DIST_DIR)/$(PKG_NAME)-linux-amd64.tar.gz \
		    $(DIST_DIR)/$(PKG_NAME)-linux-arm64.tar.gz \
		    $(DIST_DIR)/$(PKG_NAME)-windows-amd64.zip \
		    $(DIST_DIR)/install.sh \
		    $(DIST_DIR)/SHA256SUMS \
		    --clobber \
		    --repo "$(REPO)"; \
	else \
		gh release create "$(VERSION)" \
		    $(DIST_DIR)/$(PKG_NAME)-linux-amd64.tar.gz \
		    $(DIST_DIR)/$(PKG_NAME)-linux-arm64.tar.gz \
		    $(DIST_DIR)/$(PKG_NAME)-windows-amd64.zip \
		    $(DIST_DIR)/install.sh \
		    $(DIST_DIR)/SHA256SUMS \
		    --title "$(VERSION)" \
		    --notes-file "$(NOTES)" \
		    --repo "$(REPO)"; \
	fi
	@echo ">>> 上传完成"

.PHONY: release-delete
release-delete:
	@[ -n "$(VERSION)" ] || { echo "错误: 请指定 VERSION"; exit 1; }
	@[ -n "$(REPO)" ] || { echo "错误: 请指定 REPO"; exit 1; }
	@echo ">>> 删除 Release: $(VERSION) ($(REPO))"
	@gh release delete "$(VERSION)" --yes --repo "$(REPO)" || \
		echo ">>> Release 不存在或删除失败"
	@echo ">>> 已删除（tag 保留）"

# ============================================================
# 安装 / 卸载
#
#   手动模式（源码 + Go）    make install / make install-system
#   克隆模式（不需要 Go）    make install-prebuilt [SYSTEM=1] [VERSION=v1.1.5] [FROM=dist/xxx.tar.gz]
#   懒人模式（无源码无 Go）  curl 发行版里的 install.sh，或 make install-lazy
# ============================================================

.PHONY: require-go
require-go:
	@command -v go >/dev/null 2>&1 || { \
		echo "错误: 未安装 Go"; \
		echo ""; \
		echo "手动模式需要源码 + Go。不想装 Go 请改用:"; \
		echo "  make install-prebuilt     # 从 GitHub Release 装预编译包"; \
		exit 1; \
	}

.PHONY: install-files
install-files:
	$(SUDO) install -d "$(DESTDIR)$(BINDIR)"
	$(SUDO) rm -f "$(DESTDIR)$(BINDIR)/$(TUI)$(SUFFIX)" "$(DESTDIR)$(BINDIR)/$(WEB)$(SUFFIX)" \
	              "$(DESTDIR)$(BINDIR)/$(BIN)$(SUFFIX)" "$(DESTDIR)$(BINDIR)/rbackup.sh"
	$(SUDO) install -m 0755 "$(TARGET)" "$(DESTDIR)$(BINDIR)/$(TUI)$(SUFFIX)"
	$(SUDO) ln -f "$(DESTDIR)$(BINDIR)/$(TUI)$(SUFFIX)" "$(DESTDIR)$(BINDIR)/$(WEB)$(SUFFIX)" 2>/dev/null || \
	    $(SUDO) cp -f "$(DESTDIR)$(BINDIR)/$(TUI)$(SUFFIX)" "$(DESTDIR)$(BINDIR)/$(WEB)$(SUFFIX)"
	@if [ -f "$(SCRIPT_FILE)" ]; then \
		$(SUDO) install -m 0755 "$(SCRIPT_FILE)" "$(DESTDIR)$(BINDIR)/$(SCRIPT_FILE)"; \
	fi

# ---------- 手动模式：源码 + Go ----------
.PHONY: install install-user
install install-user: build install-files
	@echo ">>> 安装完成（用户级）: $(DESTDIR)$(BINDIR)/$(TUI)$(SUFFIX) / $(WEB)$(SUFFIX) / $(SCRIPT_FILE)"
	@echo ">>> 配置目录: $(HOME)/rbackup-ui/"

.PHONY: install-system
install-system: build
	@echo ">>> 系统级安装到 /usr/local/bin（需要 sudo 权限）"
	@$(MAKE) --no-print-directory install-files SUDO=sudo BINDIR=/usr/local/bin
	@echo ">>> 安装完成（系统级）"
	@echo ">>> 配置仍按每个用户: ~/rbackup-ui/config.ini"

# ---------- 克隆模式：下载预编译包，全程不调用 go ----------
.PHONY: install-prebuilt
install-prebuilt:
	@bash ./install.sh \
	    $(if $(filter 1,$(SYSTEM)),--system,) \
	    --version $(PREBUILT_VERSION) \
	    $(if $(FROM),--from "$(FROM)",)

# ---------- 懒人模式：连源码都不要（自举 Release 里的 install.sh）----------
.PHONY: install-lazy
install-lazy:
	@url="https://github.com/$(REPO)/releases/latest/download/install.sh"; \
	echo ">>> 从 Release 获取安装器: $$url"; \
	tmp=$$(mktemp); \
	if [ -n "$${GH_TOKEN:-}" ]; then \
	    curl -fsSL -H "Authorization: Bearer $$GH_TOKEN" "$$url" -o "$$tmp" \
	      || { echo "✗ 下载失败：install.sh 走的是公开下载地址，私有仓库请改用 make install-prebuilt" >&2; rm -f "$$tmp"; exit 1; }; \
	else \
	    curl -fsSL "$$url" -o "$$tmp" \
	      || { echo "✗ 下载失败：仓库可能尚未公开，或该 Release 还没有 install.sh 资产" >&2; rm -f "$$tmp"; exit 1; }; \
	fi; \
	bash "$$tmp" $(if $(filter 1,$(SYSTEM)),--system,); rc=$$?; rm -f "$$tmp"; exit $$rc

.PHONY: uninstall
uninstall:
	@user_manifest="$(HOME)/.local/share/$(BIN)/install/install-manifest.txt"; \
	sys_manifest="/usr/local/share/$(BIN)/install/install-manifest.txt"; \
	legacy_manifest="$(HOME)/.local/share/$(BIN)/install-manifest.txt"; \
	if [ -f "$$sys_manifest" ] && [ "$$(id -u)" -eq 0 ]; then \
		echo ">>> 卸载系统级安装（按安装清单）"; \
		bash ./install.sh --system --uninstall; \
	elif [ -f "$$user_manifest" ]; then \
		echo ">>> 卸载用户级安装（按安装清单，保留配置与日志）"; \
		bash ./install.sh --uninstall; \
	elif [ -f "$$legacy_manifest" ]; then \
		echo ">>> 卸载旧布局安装（清单：$$legacy_manifest，保留配置）"; \
		while IFS= read -r f; do \
			case "$$f" in ''|'#'*) continue ;; esac; \
			rm -f "$$f" && echo "  已删除 $$f"; \
		done < "$$legacy_manifest"; \
		rm -f "$$legacy_manifest"; \
		echo ">>> 完成"; \
	else \
		echo ">>> 卸载手动安装的文件（保留配置目录）"; \
		rm -f "$(DESTDIR)$(BINDIR)/$(TUI)$(SUFFIX)" \
		      "$(DESTDIR)$(BINDIR)/$(WEB)$(SUFFIX)" \
		      "$(DESTDIR)$(BINDIR)/$(BIN)$(SUFFIX)" \
		      "$(DESTDIR)$(BINDIR)/rbackup.sh" \
		      "$(DESTDIR)$(BINDIR)/$(SCRIPT_FILE)"; \
		echo ">>> 完成"; \
	fi

# ============================================================
# 其他
# ============================================================

.PHONY: tidy
tidy:
	go mod tidy

.PHONY: clean
clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR) $(STAGE_DIR)

.PHONY: version
version:
	@echo "BUILD_VERSION = $(BUILD_VERSION)"
	@echo "VERSION       = $(VERSION)"
	@echo "PKG_VERSION   = $(PKG_VERSION)"
	@echo "PKG_NAME      = $(PKG_NAME)"
	@echo "REPO          = $(REPO)"
	@echo "LDFLAGS       = $(LDFLAGS)"

.PHONY: help
help:
	@echo "rbackup-ui 构建、打包、安装"
	@echo ""
	@echo "安装 —— 三种模式:"
	@echo "  [懒人模式] 无源码、无 Go："
	@echo "      curl -fsSL https://github.com/$(REPO)/releases/latest/download/install.sh | bash"
	@echo "      curl -fsSL .../install.sh | sudo bash -s -- --system"
	@echo "      make install-lazy [SYSTEM=1]           # 同上的 Makefile 包装"
	@echo "  [克隆模式] git clone 后，不需要 Go："
	@echo "      make install-prebuilt                  # 装当前检出对应的版本"
	@echo "      make install-prebuilt SYSTEM=1         # 系统级"
	@echo "      make install-prebuilt VERSION=v1.1.5   # 指定版本"
	@echo "      make install-prebuilt FROM=dist/x.tar.gz   # 离线"
	@echo "  [手动模式] 源码 + Go："
	@echo "      make install                           # 用户级 ($(PREFIX))"
	@echo "      make install-system                    # /usr/local（sudo）"
	@echo ""
	@echo "  卸载: make uninstall"
	@echo ""
	@echo "构建:"
	@echo "  make build               编当前平台"
	@echo "  make build-win           Windows exe"
	@echo "  make build-linux         Linux amd64"
	@echo "  make build-linux-arm64   Linux arm64"
	@echo "  make build-all           全部"
	@echo ""
	@echo "打包:"
	@echo "  make package             全部平台（dist/*.tar.gz / *.zip）"
	@echo "  make package-linux       仅 Linux amd64"
	@echo "  make package-linux-arm64 仅 Linux arm64"
	@echo "  make package-win         仅 Windows amd64"
	@echo "  make checksums           生成 dist/SHA256SUMS"
	@echo ""
	@echo "发布:"
	@echo "  make release             本地打包 + 校验和（不上传）"
	@echo "  make release-notes       生成发布说明"
	@echo "  make verify-release VERSION=v1.1.6   校验已发布资产"
	@echo "  make release-upload      应急手工上传（需要 gh）"
	@echo "  make release-delete      删除 Release（保留 tag）"
	@echo ""
	@echo "其他:"
	@echo "  make clean               清空 bin/ dist/ .staging/"
	@echo "  make version             显示版本信息"
	@echo "  make check-version       校验版本解析（CI 用）"
	@echo "  make help                本帮助"
	@echo ""
	@echo "参数:"
	@echo "  VERSION=v1.1.5           指定版本（默认取 git 最近 tag）"
	@echo "  SYSTEM=1                 安装到系统级"
	@echo "  FROM=dist/x.tar.gz       离线安装用的本地包"
	@echo "  REPO=user/repo           手动指定仓库（默认从 git remote 解析）"
