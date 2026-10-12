@echo off
setlocal
cd /d "%~dp0portable"
if not defined SPACE_SHERIFF_DATA_DIR set "SPACE_SHERIFF_DATA_DIR=%~dp0portable\.local-data"
go run .
if errorlevel 1 pause
