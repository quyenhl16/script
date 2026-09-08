# Post-deployment validation artifacts

Before running the `post-deployment-validation` workflow, copy the Excel
baseline to this directory using this exact name:

```text
pramf01-input_100K.xlsx
```

The local workflow exposes the workbook and matching Python checker paths to
the resource and environment steps. These files are read-only inputs and are
not copied to a remote server.
