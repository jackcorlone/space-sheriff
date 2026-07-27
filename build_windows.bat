@echo off
setlocal
cd /d "%~dp0"
py -3 -m venv work\build-venv-windows
call work\build-venv-windows\Scripts\activate.bat
python -m pip install -r requirements-build.txt
python -m unittest discover -s tests -v
pyinstaller --noconfirm --clean SpaceSheriff.spec
echo.
echo Build complete: dist\SpaceSheriff.exe
pause
