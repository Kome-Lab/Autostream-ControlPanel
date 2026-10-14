"""Run all independent same-run analyses before returning the aggregate exit."""
import pathlib,runpy
runpy.run_path(str(pathlib.Path(__file__).with_name("media_independent.py")),run_name="__main__")
