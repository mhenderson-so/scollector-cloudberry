# scollector-cloudberry
External collector for Bosun's scollector for monitoring CloudBerry Backup Enterprise Edition

There are no configuration options for this external collector yet.

It collects the following statistics:

- Number of backup jobs configured
- The number of files uploaded in each job
- The duration of each job
- The time since each job last started
- The amount of data that the last job uploaded
- The total size of the data of the last job (i.e. the size of the original backup set, not just what was backed up)

It works by reading the .cbb files found in the CloudBerry data files (which are XML files with the plan details),
and by querying the SQLite database that contains the CloudBerry backup history.

Due to the limited set of characters that are valid as OpenTSDB tag values, some backup
plan names will have characters subtituted or stripped from their names in Bosun.

##Installation

To use the collector, you need to place it in the external collectors folder of your scollector instance,
inside a folder named with the number of seconds between each run.

e.g. If your scollector lives at `C:\Program Files\scollector`, and you want to query your CloudBerry instance 
every 90 seconds, you would put the EXE at `C:\Program Files\scollector\collectors\90\scollector-cloudberry.exe`