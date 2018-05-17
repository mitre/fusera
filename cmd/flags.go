package cmd

// func NewApp() (app *cli.App, cmd *Commands) {
// 	cmd = &Commands{}
// 	app = &cli.App{
// 		Commands: []cli.Command{
// 			{
// 				Name:    "mount",
// 				Aliases: []string{"m"},
// 				Usage:   "to mount a folder",
// 				Action: func(c *cli.Context) error {
// 					twig.SetDebug(c.IsSet("debug"))
// 					// Populate and parse flags.
// 					flags, err := PopulateMountFlags(c)
// 					if err != nil {
// 						cause := errors.Cause(err)
// 						if os.IsPermission(cause) {
// 							fmt.Print("\nSeems like fusera doesn't have permissions to read a file!")
// 							fmt.Printf("\nTry changing the permissions with chmod +r path/to/file\n")
// 						}
// 						fmt.Printf("\ninvalid arguments: %s\n\n", errors.Cause(err))
// 						twig.Debugf("%+#v", err.Error())
// 						return err
// 					}
// 					twig.Debugf("accs: %s", flags.Acc)
// 					cmd.Flags = flags
// 					return nil
// 				},
// 			},
// 		},
// 	}

// 	return
// }
