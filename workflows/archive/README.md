# Archived workflows

These are the pre-matrix STOR-HY workflows, mostly sequential chains of the
FMI 2.0 replica FMUs under `create_fmu/storhy_replicas/`, plus the La Rance
FMI 3.0 sediment/cleaning event demo. They were retired when the demo was
rescoped to two demonstrators (Cheylas and Alqueva) with one FMI 3.0 FMU per
model family. They are kept, with their original relative paths under
`archive/`, for regression checks and reference, and are hidden from the
dashboard catalog.

`demonstrators/la_rance/maintenance/sediment_cleaning_events.yaml` uses the
retired `CleaningDecisionFmi3` FMU and the old `SedimentExposureFmi3`
interface (`cleanings_done`, `tidal_head_m`), so it no longer runs against the
current FMUs; its pattern lives on in
`workflows/demonstrators/cheylas/monitoring/sediment_erosion_events.yaml`.

All models are deterministic placeholders, not validated engineering models.
