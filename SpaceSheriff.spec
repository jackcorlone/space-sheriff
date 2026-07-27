# -*- mode: python ; coding: utf-8 -*-
import sys

a = Analysis(
    ["app.py"],
    pathex=[],
    binaries=[],
    datas=[],
    hiddenimports=[],
    hookspath=[],
    hooksconfig={},
    runtime_hooks=[],
    excludes=[],
    noarchive=False,
)
pyz = PYZ(a.pure)
exe = EXE(
    pyz,
    a.scripts,
    a.binaries if sys.platform != "darwin" else [],
    a.datas if sys.platform != "darwin" else [],
    [],
    name="SpaceSheriff",
    debug=False,
    bootloader_ignore_signals=False,
    strip=False,
    upx=True,
    console=False,
    exclude_binaries=sys.platform == "darwin",
)
if sys.platform == "darwin":
    collect = COLLECT(
        exe,
        a.binaries,
        a.datas,
        strip=False,
        upx=True,
        name="SpaceSheriff",
    )
    app = BUNDLE(
        collect,
        name="SpaceSheriff.app",
        icon=None,
        bundle_identifier="com.spacesheriff.desktop",
    )
